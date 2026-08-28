// Package api — agent-facing endpoints.
//
// Mounted under /api/v1/agent/* and gated by RequireAgent. Agents
// authenticate via Authorization: Bearer <api-token> and the handlers
// resolve the agent id from the context (set by RequireAgent).
package api

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/go-chi/chi/v5"
	"net/http"
	"time"

	"github.com/ramgml/orenda/internal/api/ws"

	activity "github.com/ramgml/orenda/internal/domain/activity"
	"github.com/ramgml/orenda/internal/domain/task"
	taskservice "github.com/ramgml/orenda/internal/service/task"
)

// agentMeHandler returns the agent record bound to the bearer token.
func agentMeHandler(deps *Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := IdentityFrom(r.Context())
		if !ok || id.AgentID == "" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		a, err := deps.Agents.GetByID(r.Context(), id.AgentID)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, a)
	}
}

// agentHeartbeatHandler updates last_seen_at for the bearer agent.
func agentHeartbeatHandler(deps *Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := IdentityFrom(r.Context())
		if !ok || id.AgentID == "" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		a, err := deps.Agents.TouchLastSeen(r.Context(), id.AgentID)
		if err != nil {
			writeError(w, err)
			return
		}
		if deps.WSHub != nil {
			deps.WSHub.Publish(r.Context(), ws.Event{
				Topic: "agents",
				Body:  map[string]any{"type": "agent.heartbeat", "agent": a},
			})
		}
		writeJSON(w, http.StatusOK, a)
	}
}

// agentClaimRequest is the (small) body for POST /agent/tasks/{id}/claim.
type agentClaimRequest struct {
	Note string `json:"note"`
}

// agentClaimTaskHandler claims a task for the bearer agent. The agent_id
// comes from the bearer token, NOT from the request body.
func agentClaimTaskHandler(deps *Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := IdentityFrom(r.Context())
		if !ok || id.AgentID == "" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if deps.TaskService == nil {
			http.Error(w, "task service not wired", http.StatusServiceUnavailable)
			return
		}
		// Body is optional (currently just a note).
		var req agentClaimRequest
		_ = json.NewDecoder(r.Body).Decode(&req)

		// Task numbers ("T42") resolve to the UUID here; every
		// downstream call (claim, lock-holder lookup) works on ids.
		taskID, rerr := resolveTaskRef(r.Context(), deps, chi.URLParam(r, "id"))
		if rerr != nil {
			writeResolveError(w, rerr)
			return
		}
		tr, err := deps.TaskService.Claim(r.Context(), taskID, id.AgentID)
		if err != nil {
			if errors.Is(err, taskservice.ErrLockTaken) {
				// Phase 15: extend 409 with the current holder
				// (agent_id / agent_name / claimed_at) when the
				// TaskLockHolder seam is wired. Bare
				// {"error":"lock_taken"} is the backwards-compatible
				// fallback when the lookup fails or returns empty.
				writeJSON(w, http.StatusConflict, lockTakenResponse(deps, r.Context(), taskID))
				return
			}
			// Phase 15.3: 422 with the unfinished blockers list so
			// the agent knows exactly what's still outstanding.
			var blocked *taskservice.BlockedError
			if errors.As(err, &blocked) {
				writeJSON(w, http.StatusUnprocessableEntity, map[string]any{
					"error":               "task_blocked",
					"unfinished_blockers": blocked.BlockerIDs,
				})
				return
			}
			if errors.Is(err, taskservice.ErrNotFound) {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": "not_found"})
				return
			}
			writeError(w, err)
			return
		}
		// Notify owner: agent picked this up.
		notifyTaskAssignee(r.Context(), deps, "task.assigned_to_me",
			"task.assigned_to_me:"+tr.ID, tr, id.AgentID)
		writeJSON(w, http.StatusOK, tr)
	}
}

// agentReleaseTaskHandler releases a task the bearer agent holds.
func agentReleaseTaskHandler(deps *Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := IdentityFrom(r.Context())
		if !ok || id.AgentID == "" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if deps.TaskService == nil {
			http.Error(w, "task service not wired", http.StatusServiceUnavailable)
			return
		}
		taskID, rerr := resolveTaskRef(r.Context(), deps, chi.URLParam(r, "id"))
		if rerr != nil {
			writeResolveError(w, rerr)
			return
		}
		tr, err := deps.TaskService.Release(r.Context(), taskID, id.AgentID)
		if err != nil {
			writeError(w, err)
			return
		}
		// Notify owner: agent released the task.
		notifyTaskAssignee(r.Context(), deps, "task.released",
			"task.released:"+tr.ID, tr, id.AgentID)
		writeJSON(w, http.StatusOK, tr)
	}
}

// agentSubmitTaskHandler submits a task for human review.
func agentSubmitTaskHandler(deps *Dependencies) http.HandlerFunc {
	type req struct {
		Note string `json:"note"`
	}
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := IdentityFrom(r.Context())
		if !ok || id.AgentID == "" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if deps.TaskService == nil {
			http.Error(w, "task service not wired", http.StatusServiceUnavailable)
			return
		}
		var in req
		_ = json.NewDecoder(r.Body).Decode(&in)
		taskID, rerr := resolveTaskRef(r.Context(), deps, chi.URLParam(r, "id"))
		if rerr != nil {
			writeResolveError(w, rerr)
			return
		}
		// Task 87: a submit must carry evidence of time spent. The
		// auto-timer keeps an entry open while the task is in
		// in_progress, so an open timer counts; otherwise the task's
		// accrued total must be non-zero (manual entries included).
		if err := checkTimeLogged(r.Context(), deps, taskID, id.AgentID); err != nil {
			writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "time_not_logged"})
			return
		}
		tr, err := deps.TaskService.Submit(r.Context(), taskID, id.AgentID, in.Note)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, tr)
	}
}

// agentTaskContextHandler returns a context snapshot for the bearer
// agent (same shape as the user-facing /context endpoint).
func agentTaskContextHandler(deps *Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := IdentityFrom(r.Context())
		if !ok || id.AgentID == "" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		ctx := r.Context()
		// "T42" resolves to the UUID; the snapshot below then keys
		// everything (comments, activity, children, locks) off the id.
		taskID, rerr := resolveTaskRef(ctx, deps, chi.URLParam(r, "id"))
		if rerr != nil {
			writeResolveError(w, rerr)
			return
		}
		tr, err := deps.Tasks.GetByID(ctx, taskID)
		if err != nil {
			writeError(w, err)
			return
		}
		scrubTaskForNonHolder(tr, id.AgentID)
		out := &TaskContext{Task: tr}
		if deps.Comments != nil {
			out.Comments, _ = deps.Comments.ListByTarget(ctx, "task", taskID)
		}
		if deps.Activities != nil {
			acts, _ := deps.Activities.ListByTask(ctx, taskID)
			out.Activity = filterActivityForAgent(acts, id.AgentID, *tr)
		}
		if children, err := deps.Tasks.ListChildren(ctx, taskID); err == nil {
			out.Children = children
		}
		if cls, err := deps.Tasks.ListChecklists(ctx, taskID); err == nil {
			out.Checklists = cls
			out.ChecklistItems = map[string][]task.ChecklistItemRow{}
			for _, cl := range cls {
				if its, err := deps.Tasks.ListChecklistItems(ctx, cl.ID); err == nil {
					out.ChecklistItems[cl.ID] = its
				}
			}
		}
		// Phase 15: same shape as the user-facing /context — open
		// blockers + current lock holder. The bearer-agent
		// snapshot is otherwise identical, so we reuse the same
		// helpers in handlers_phase3.go.
		populateContextBlockers(deps, ctx, taskID, out)
		populateContextLockHolder(deps, ctx, taskID, out)
		writeJSON(w, http.StatusOK, out)
	}
}

// scrubTaskForNonHolder clears per-claim private fields when the
// caller is not the task's current assignee. Task is a value type —
// the mutation lands on the caller's copy, not the cache.
func scrubTaskForNonHolder(tr *task.Task, agentID string) {
	if tr.AssigneeType == task.AssigneeAgent && tr.AssigneeID == agentID {
		return // holder reads everything
	}
	tr.AgentNotes = ""
	tr.ContextMD = ""
}

// filterActivityForAgent strips rows that leak holder-private content
// from the activity feed of a non-holder reader.
func filterActivityForAgent(acts []*activity.Activity, agentID string, tr task.Task) []*activity.Activity {
	holder := tr.AssigneeType == task.AssigneeAgent && tr.AssigneeID == agentID
	out := make([]*activity.Activity, 0, len(acts))
	for _, a := range acts {
		if a.Action == activity.ActionAgentNotes && !holder {
			continue
		}
		out = append(out, a)
	}
	return out
}

// checkTimeLogged implements the Task 87 submit gate: the task must
// carry evidence of spent time before it can enter review. Time
// counts when the task's accrued total (tasks.time_spent_s — fed by
// the auto-timer close and manual entries) is non-zero, or when the
// agent still has an open timer on this task (an in_progress claim
// without a single closed interval yet).
//
// A nil TimeService (partial installs without the timer routes)
// fails open — the gate degrades to the pre-T87 behaviour.
func checkTimeLogged(ctx context.Context, deps *Dependencies, taskID, agentID string) error {
	tr, err := deps.Tasks.GetByID(ctx, taskID)
	if err != nil {
		return err
	}
	if tr.TimeSpentS > 0 {
		return nil
	}
	if deps.TimeService == nil {
		return nil
	}
	open, err := deps.TimeService.OpenFor(ctx, agentID, taskID)
	if err != nil {
		return err
	}
	if open != nil {
		return nil
	}
	return errors.New("time_not_logged")
}

// agentAddManualTimeHandler records time the auto-timer missed
// (offline work, imports, "this took no real time" zeroes). Minutes
// must be >= 0; 0 is valid and is the documented bypass for trivial
// tasks that must still pass the submit gate.
func agentAddManualTimeHandler(deps *Dependencies) http.HandlerFunc {
	type req struct {
		Minutes float64 `json:"minutes"`
		Note    string  `json:"note"`
	}
	return func(w http.ResponseWriter, r *http.Request) {
		if deps.TimeService == nil {
			http.Error(w, "time service not wired", http.StatusServiceUnavailable)
			return
		}
		id, ok := IdentityFrom(r.Context())
		if !ok || id.AgentID == "" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		var in req
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_json"})
			return
		}
		if in.Minutes < 0 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "minutes_must_be_non_negative"})
			return
		}
		taskID, rerr := resolveTaskRef(r.Context(), deps, chi.URLParam(r, "id"))
		if rerr != nil {
			writeResolveError(w, rerr)
			return
		}
		end := time.Now().UTC()
		start := end.Add(-time.Duration(in.Minutes * float64(time.Minute)))
		got, err := deps.TimeService.ManualAdd(r.Context(), taskID, id.AgentID, start, end)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, got)
	}
}
