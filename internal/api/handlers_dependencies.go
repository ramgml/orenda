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
//	GET /api/v1/agent/tasks?ready=true   only claimable tasks
//	GET /api/v1/agent/tasks?limit=100    cap (default 100, max 500)
//
// "ready" means: status NOT IN (done, review, in_progress) AND
// no unfinished blockers AND no current lock holder. Useful for
// the agent inbox: "what can I claim right now?". Without ?ready
// the response is the full list — the agent then has to do the
// filtering itself.
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

		// Single pass: list every "claimable" status task, then
		// hydrate blockers with one batch query and filter. For a
		// single-owner install the whole list is < few hundred.
		// Phase 33.1: AssigneeTypeIncludeNull — an unassigned todo
		// task (e.g. an agent-proposed task the owner just triaged
		// from the review queue onto the board) is claimable by any
		// agent, so it belongs on this surface.
		f := task.Filter{Status: task.StatusTodo, AssigneeType: task.AssigneeAgent, AssigneeTypeIncludeNull: true}
		tasks, err := deps.Tasks.ListByProject(r.Context(), f)
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
