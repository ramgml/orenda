// Package api — Phase 15 handlers for task dependencies and the
// agent-facing tasks listing.
//
// Two endpoints:
//
//	PUT /api/v1/tasks/{id}/dependencies   user-side, replace blocker set
//	GET /api/v1/tasks/{id}/blockers       user-side, read current blockers
//	GET /api/v1/tasks/{id}/dependents     user-side, read reverse edges
//	GET /api/v1/agent/tasks                agent-side, list with ready/blocked filter
//
// The PUT endpoint runs the cycle check (service.SetTaskDependencies)
// before mutating; cycles + self-dependencies surface as 422.
package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	taskservice "github.com/ramgml/orenda/internal/service/task"

	"github.com/ramgml/orenda/internal/domain/project"
	"github.com/ramgml/orenda/internal/domain/task"
)

// dependenciesRequest is the body of PUT /tasks/{id}/dependencies.
type dependenciesRequest struct {
	DependsOnIDs []string `json:"depends_on_ids"`
}

// putTaskDependenciesHandler replaces the task's full blocker set.
//
//	{ "depends_on_ids": ["task-A", "task-B"] }   — replace
//	{ "depends_on_ids": [] }                     — clear all
//
// Returns 200 with the new blockers list (Phase 15.4: the WS event
// already invalidates client caches; we still echo the new state so
// callers don't have to re-fetch).
func putTaskDependenciesHandler(deps *Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps.TaskService == nil || deps.Tasks == nil {
			http.Error(w, "task deps not wired", http.StatusServiceUnavailable)
			return
		}
		taskID := chi.URLParam(r, "id")
		var req dependenciesRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_json"})
			return
		}
		// Allow missing body → empty list, but reject explicit null.
		if req.DependsOnIDs == nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "depends_on_ids_required"})
			return
		}
		if err := deps.TaskService.SetTaskDependencies(r.Context(), taskID, req.DependsOnIDs); err != nil {
			// Cycle / self-dependency from the service: 422.
			if errors.Is(err, taskservice.ErrDependencyCycle) ||
				errors.Is(err, taskservice.ErrSelfDependency) {
				writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "invalid_dependency"})
				return
			}
			if errors.Is(err, taskservice.ErrNotFound) || errors.Is(err, task.ErrNotFound) {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": "not_found"})
				return
			}
			writeError(w, err)
			return
		}
		blockers, err := deps.Tasks.Blockers(r.Context(), taskID)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"blockers": blockers})
	}
}

// getTaskBlockersHandler returns ALL blockers (open + satisfied).
//
// Phase 17 wires the TaskCard's "blocked" badge through this
// endpoint via BlockedByList; Phase 27.8 lets the agent claim-flow
// surface unfinished blockers in the 422 response. Future work
// (filtering by project, etc.) builds on this same passthrough.
func getTaskBlockersHandler(deps *Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps.Tasks == nil {
			http.Error(w, "task repo not wired", http.StatusServiceUnavailable)
			return
		}
		taskID := chi.URLParam(r, "id")
		blockers, err := deps.Tasks.Blockers(r.Context(), taskID)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"blockers": blockers})
	}
}

// getTaskDependentsHandler returns the list of task ids that depend
// on this task (reverse lookup). Useful for "finishing this unblocks
// N tasks" in the UI.
func getTaskDependentsHandler(deps *Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps.Tasks == nil {
			http.Error(w, "task repo not wired", http.StatusServiceUnavailable)
			return
		}
		taskID := chi.URLParam(r, "id")
		ids, err := deps.Tasks.Dependents(r.Context(), taskID)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"dependents": ids})
	}
}

// listAgentTasksHandler returns the bearer agent's work surface.
//
//	GET /api/v1/agent/tasks?ready=true        only claimable tasks
//	GET /api/v1/agent/tasks?limit=100         cap (default 100, max 500)
//	GET /api/v1/agent/tasks?project_id=<uuid> scope to one project
//	GET /api/v1/agent/tasks?project=7|P7|<uuid>
//
// "ready" means: status NOT IN (done, review, in_progress) AND
// no unfinished blockers AND no current lock holder. Useful for
// the agent inbox: "what can I claim right now?". Without ?ready
// the response is the full list — the agent then has to do the
// filtering itself.
//
// Task 140 (agent-project-scope): the surface only carries tasks the
// agent may actually act on. A project counts as accessible when it
// is open to all agents (agents_allowed = 1) or the agent holds an
// explicit grant row. Tasks of inaccessible projects are filtered
// out; inbox tasks (no project) stay visible. ?project_id accepts a
// UUID, ?project a number ("7"), a P-number ("P7"/"p7") or a UUID;
// passing both is a 400, an unresolvable reference a 404. A project
// filter doubles as the existence check — an inaccessible project is
// indistinguishable from an empty one.
func listAgentTasksHandler(deps *Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := IdentityFrom(r.Context())
		if !ok || id.AgentID == "" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if deps.Tasks == nil {
			http.Error(w, "task repo not wired", http.StatusServiceUnavailable)
			return
		}

		limit := 100
		if v := r.URL.Query().Get("limit"); v != "" {
			if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 500 {
				limit = n
			}
		}
		readyOnly := r.URL.Query().Get("ready") == "true"

		// Task 140: optional project scope. Both parameters at once
		// is ambiguous input; the reference forms mirror the path
		// resolution elsewhere (resolveProjectRef + GetByNumber).
		projectIDParam := r.URL.Query().Get("project_id")
		projectParam := r.URL.Query().Get("project")
		if projectIDParam != "" && projectParam != "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_input"})
			return
		}
		var scopedProject *project.Project
		if projectIDParam != "" {
			p, err := deps.Projects.GetProject(r.Context(), projectIDParam)
			if err != nil {
				writeProjectResolveError(w, err)
				return
			}
			scopedProject = p
		} else if projectParam != "" {
			var err error
			if n, ok := project.ParseProjectRef(projectParam); ok {
				scopedProject, err = deps.Projects.GetByNumber(r.Context(), n)
			} else if allDigits(projectParam) {
				// Bare number form ("project=7") — ParseProjectRef
				// only covers the P-prefixed spelling.
				n, aerr := strconv.Atoi(projectParam)
				if aerr != nil || n <= 0 {
					writeJSON(w, http.StatusNotFound, map[string]string{"error": "not_found"})
					return
				}
				scopedProject, err = deps.Projects.GetByNumber(r.Context(), n)
			} else {
				scopedProject, err = deps.Projects.GetProject(r.Context(), projectParam)
			}
			if err != nil {
				writeProjectResolveError(w, err)
				return
			}
		}

		// Task 140: access set once per request — the visibility
		// filter and the project-scoped lookup share it.
		accessSet, err := deps.Projects.AgentAccessibleProjectIDs(r.Context(), id.AgentID)
		if err != nil {
			writeError(w, err)
			return
		}
		if scopedProject != nil && !accessSet[scopedProject.ID] {
			// An inaccessible project looks empty — do not leak its
			// existence or task count.
			writeJSON(w, http.StatusOK, map[string]any{"tasks": []any{}, "count": 0})
			return
		}

		// Single pass: list every "claimable" status task, then
		// hydrate blockers with one batch query and filter. For a
		// single-owner install the whole list is < few hundred.
		// Phase 33.1: AssigneeTypeIncludeNull — an unassigned todo
		// task (e.g. an agent-proposed task the owner just triaged
		// from the review queue onto the board) is claimable by any
		// agent, so it belongs on this surface.
		//
		// Task 140: with an explicit project scope one query returns
		// exactly that project's claimable tasks (inbox is NOT
		// appended — a scoped listing never mixes in no-project
		// tasks). Without a scope the full surface is listed, minus
		// tasks of projects this agent cannot access; inbox tasks
		// (ProjectID == "") always stay visible.
		var tasks []*task.Task
		if scopedProject != nil {
			f := task.Filter{ProjectID: scopedProject.ID, Status: task.StatusTodo, AssigneeType: task.AssigneeAgent, AssigneeTypeIncludeNull: true}
			var err error
			tasks, err = deps.Tasks.ListByProject(r.Context(), f)
			if err != nil {
				writeError(w, err)
				return
			}
		} else {
			f := task.Filter{Status: task.StatusTodo, AssigneeType: task.AssigneeAgent, AssigneeTypeIncludeNull: true}
			var err error
			tasks, err = deps.Tasks.ListByProject(r.Context(), f)
			if err != nil {
				writeError(w, err)
				return
			}
			// Also include inbox tasks: Filter has NoProject for that.
			f2 := task.Filter{NoProject: true, Status: task.StatusTodo}
			inboxTasks, err := deps.Tasks.ListByProject(r.Context(), f2)
			if err != nil {
				writeError(w, err)
				return
			}
			tasks = append(tasks, inboxTasks...)
		}

		type row struct {
			Task      *task.Task `json:"task"`
			BlockedBy []string   `json:"blocked_by"`
			Ready     bool       `json:"ready"`
		}
		// Phase 28.22: batch the blocker lookup — one round-trip for
		// the whole list instead of a per-task N+1.
		ids := make([]string, 0, len(tasks))
		for _, tr := range tasks {
			ids = append(ids, tr.ID)
		}
		blockersByTask, err := deps.Tasks.BlockersForTasks(r.Context(), ids)
		if err != nil {
			writeError(w, err)
			return
		}
		out := make([]row, 0, len(tasks))
		for _, tr := range tasks {
			// Task 140: project tasks of inaccessible projects are
			// invisible; inbox tasks (no project) are always shown.
			if tr.ProjectID != "" && !accessSet[tr.ProjectID] {
				continue
			}
			var blockedBy []string
			ready := true
			for _, b := range blockersByTask[tr.ID] {
				if !b.Done {
					blockedBy = append(blockedBy, b.BlockerID)
					ready = false
				}
			}
			// Task 115: status=blocked coincides with "has unfinished
			// blockers" by construction (the auto-block flips it), but a
			// legacy/rolled-back row could carry one without the other —
			// exclude on BOTH so the ready list never lies.
			if ready && tr.Status == task.StatusBlocked {
				ready = false
			}
			// "ready" excludes tasks already claimed (by anyone) AND
			// tasks assigned to a different agent. Phase 15: we also
			// exclude tasks assigned to the calling agent itself —
			// the agent shouldn't see its own in-flight tasks in the
			// ready list (that's noise; the agent already knows it
			// has them).
			if ready && tr.AssigneeType == task.AssigneeAgent && tr.AssigneeID != "" && tr.AssigneeID != id.AgentID {
				ready = false
			}
			if ready && tr.AssigneeType == task.AssigneeAgent && tr.AssigneeID == id.AgentID {
				ready = false
			}
			if readyOnly && !ready {
				continue
			}
			out = append(out, row{Task: tr, BlockedBy: blockedBy, Ready: ready})
		}
		if len(out) > limit {
			out = out[:limit]
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"tasks": out,
			"count": len(out),
		})
	}
}

// allDigits reports whether s is a non-empty ASCII digit sequence —
// the bare-number spelling of a project reference ("project=7").
// ParseProjectRef covers only the "P7" form.
func allDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}
