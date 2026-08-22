// Package api — Phase 33.1 + 33.3: agent-side task creation.
//
//	POST /api/v1/agent/tasks — the agent proposes a NEW task.
//
// The DOGFOOD convention ("new work = a task in the instance") was
// not executable by agents: the user-side create endpoint sits under
// RequireUser (cookie), so a bearer token got 401. This handler is
// the agent-namespace twin: it creates a REAL task (no new table,
// no new status) that lands as status=backlog + awaiting=none, on
// the backlog column so it is visible on the kanban. The owner
// triages it on the board: a drag to a real-work column (todo /
// in_progress / done) clears awaiting via Service.Move and the task
// becomes claimable by agents; dismiss = DELETE /api/v1/tasks/{id}.
//
// Phase 33.3 dropped awaiting=human on propose: the review queue is
// now reserved for agent-submitted work (status=review), not for
// backlog triage. Backlog tasks are still NOT claimable from
// /api/v1/agent/tasks (the listing filters to Status=todo) — the
// agent can only pick them up after the owner drags them out of
// backlog.
package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"go.uber.org/zap"

	"github.com/ramgml/orenda/internal/api/ws"
	"github.com/ramgml/orenda/internal/domain/activity"
	"github.com/ramgml/orenda/internal/domain/task"
	taskservice "github.com/ramgml/orenda/internal/service/task"
)

// agentProposeTaskRequest is the body of POST /api/v1/agent/tasks.
// project_id, title and description_md are mandatory — a task without
// a written intent fails the CONTEXT.md "self-sufficient task" bar.
// priority / blocked_by / parent_task_id are optional conveniences.
type agentProposeTaskRequest struct {
	ProjectID     string   `json:"project_id"`
	Title         string   `json:"title"`
	DescriptionMD string   `json:"description_md"`
	Priority      string   `json:"priority"`
	BlockedBy     []string `json:"blocked_by"`
	ParentTaskID  string   `json:"parent_task_id"`
}

// agentCreateTaskHandler creates a task proposed by the bearer agent.
//
// Status codes: 201 created; 400 missing/invalid fields; 404 unknown
// project / parent / blocker (the agent namespace is single-owner, so
// "not found" also covers "not yours"); 422 invalid dependency graph;
// 401 is enforced by RequireAgent before this handler runs.
func agentCreateTaskHandler(deps *Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := IdentityFrom(r.Context())
		if !ok || id.AgentID == "" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if deps.Tasks == nil || deps.Projects == nil {
			http.Error(w, "task repo not wired", http.StatusServiceUnavailable)
			return
		}
		var in agentProposeTaskRequest
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_json"})
			return
		}
		in.ProjectID = strings.TrimSpace(in.ProjectID)
		in.Title = strings.TrimSpace(in.Title)
		if in.ProjectID == "" || in.Title == "" || strings.TrimSpace(in.DescriptionMD) == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_input"})
			return
		}
		// Resolve project ref (P<N> or UUID) to UUID.
		resolved, err := resolveProjectRef(r.Context(), deps, in.ProjectID)
		if err != nil {
			writeProjectResolveError(w, err)
			return
		}
		in.ProjectID = resolved.ID
		prio := task.PriorityMedium
		if in.Priority != "" {
			switch task.Priority(in.Priority) {
			case task.PriorityLow, task.PriorityMedium, task.PriorityHigh, task.PriorityUrgent:
				prio = task.Priority(in.Priority)
			default:
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_input"})
				return
			}
		}
		// Referenced tasks must exist — surface a clean 404 instead of
		// an opaque FK violation from the insert below.
		if in.ParentTaskID != "" {
			if _, err := deps.Tasks.GetByID(r.Context(), in.ParentTaskID); err != nil {
				writeError(w, err)
				return
			}
		}
		for _, blockerID := range in.BlockedBy {
			if _, err := deps.Tasks.GetByID(r.Context(), blockerID); err != nil {
				writeError(w, err)
				return
			}
		}

		// Phase 33.3: await=none by default — the owner's triage surface
		// for a proposed task is the kanban backlog, not the review
		// queue (which is now reserved for agent-submitted work). task.Validate
		// fills Awaiting=none when the field is left empty, so we just
		// don't set it here.
		tr := &task.Task{
			ProjectID:     in.ProjectID,
			ParentTaskID:  in.ParentTaskID,
			Title:         in.Title,
			Description:   in.DescriptionMD,
			Status:        task.StatusBacklog,
			Priority:      prio,
			CreatedByType: task.CreatorAgent,
			CreatedByID:   id.AgentID,
		}
		// Land on the board's backlog column so the card is visible in
		// the kanban for triage. A project without a backlog-status
		// column leaves the card off-board (same best-effort contract
		// as syncColumnToStatus).
		if col, err := deps.Projects.FindColumnByStatus(r.Context(), in.ProjectID, string(task.StatusBacklog)); err == nil && col != nil {
			tr.ColumnID = col.ID
		}

		if err := deps.Tasks.Create(r.Context(), tr); err != nil {
			writeError(w, err)
			return
		}

		if len(in.BlockedBy) > 0 {
			if deps.TaskService == nil {
				http.Error(w, "task service not wired", http.StatusServiceUnavailable)
				return
			}
			if err := deps.TaskService.SetTaskDependencies(r.Context(), tr.ID, in.BlockedBy); err != nil {
				switch {
				case errors.Is(err, taskservice.ErrSelfDependency),
					errors.Is(err, taskservice.ErrDependencyCycle),
					errors.Is(err, taskservice.ErrDependencyExists):
					writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "invalid_dependency"})
				default:
					writeError(w, err)
				}
				return
			}
		}

		if deps.TaskService != nil {
			deps.TaskService.MirrorSave(r.Context(), tr)
		}

		// Audit "who proposed this" — the review queue shows the task,
		// the activity feed shows the agent that filed it. Best-effort:
		// an audit glitch must not fail the user-visible create (same
		// convention as the comment/attachment recorders, Phase 28.5).
		if deps.ActivityRecorder != nil {
			payload := fmt.Sprintf(`{"project_id":%q,"title":%q}`, tr.ProjectID, tr.Title)
			if err := deps.ActivityRecorder.RecordTask(r.Context(), tr.ID,
				activity.ActorAgent, id.AgentID, activity.ActionCreated, payload); err != nil && deps.Logger != nil {
				deps.Logger.Warn("agent task activity record failed",
					zap.String("task_id", tr.ID), zap.Error(err))
			}
		}

		if deps.WSHub != nil {
			deps.WSHub.Publish(r.Context(), ws.Event{
				Topic: "tasks",
				Body: map[string]any{
					"type":  "task.created",
					"task":  tr,
					"actor": id.AgentID,
				},
			})
		}

		writeJSON(w, http.StatusCreated, tr)
	}
}
