// Package api — Task 115: single-edge blocker endpoints.
//
//	POST   /api/v1/tasks/{id}/blocks                add one blocker
//	DELETE /api/v1/tasks/{id}/blocks/{blockedBy}    remove one blocker
//
// Both live under the same router subtree + cookie auth as the Phase
// 15 PUT /dependencies endpoint and accept UUIDs or T<N> refs for
// every task argument (resolveTaskRef). The service layer
// (task.AddBlocker / task.RemoveBlocker) owns validation and the
// auto-block state machine:
//
//	target unknown            → 404
//	blocker unknown           → 404
//	self-block                → 422 invalid_dependency (same code as self-loop)
//	cycle                     → 422 invalid_dependency
//	unknown edge on DELETE    → 404
//	edge already exists (POST) → idempotent 200, not an error
//
// The response always carries the refreshed blockers list AND the
// resulting task so the UI can flip the status badge without a
// second round-trip.
package api

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	taskservice "github.com/ramgml/orenda/internal/service/task"

	"github.com/ramgml/orenda/internal/domain/task"
)

// addBlockerRequest is the body of POST /tasks/{id}/blocks.
type addBlockerRequest struct {
	BlockedBy string `json:"blocked_by"`
}

// blocksResponse is the shared response shape of both single-edge
// endpoints: the refreshed blocker list plus the resulting task
// (status may have auto-flipped to/from `blocked`).
type blocksResponse struct {
	Blockers []task.BlockerRow `json:"blockers"`
	Task     *task.Task        `json:"task"`
}

// postTaskBlockHandler adds ONE blocker edge and returns the updated
// blockers + resulting task status.
func postTaskBlockHandler(deps *Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps.TaskService == nil || deps.Tasks == nil {
			http.Error(w, "task deps not wired", http.StatusServiceUnavailable)
			return
		}
		targetRef := chi.URLParam(r, "id")
		var req addBlockerRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.BlockedBy == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "blocked_by_required"})
			return
		}
		targetID, rerr := resolveTaskRef(r.Context(), deps, targetRef)
		if rerr != nil {
			writeResolveError(w, rerr)
			return
		}
		blockerID, rerr := resolveTaskRef(r.Context(), deps, req.BlockedBy)
		if rerr != nil {
			// Brief requires "task not found" for an unknown blocker;
			// the resolver's 404 text already says exactly that.
			writeResolveError(w, rerr)
			return
		}
		tr, err := deps.TaskService.AddBlocker(r.Context(), targetID, blockerID)
		if err != nil {
			writeBlocksError(w, err)
			return
		}
		writeBlocksOK(w, r, deps, targetID, tr)
	}
}

// deleteTaskBlockHandler removes ONE blocker edge and returns the
// updated blockers + resulting task status.
func deleteTaskBlockHandler(deps *Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps.TaskService == nil || deps.Tasks == nil {
			http.Error(w, "task deps not wired", http.StatusServiceUnavailable)
			return
		}
		targetID, rerr := resolveTaskRef(r.Context(), deps, chi.URLParam(r, "id"))
		if rerr != nil {
			writeResolveError(w, rerr)
			return
		}
		blockerID, rerr := resolveTaskRef(r.Context(), deps, chi.URLParam(r, "blockedBy"))
		if rerr != nil {
			writeResolveError(w, rerr)
			return
		}
		tr, err := deps.TaskService.RemoveBlocker(r.Context(), targetID, blockerID)
		if err != nil {
			writeBlocksError(w, err)
			return
		}
		writeBlocksOK(w, r, deps, targetID, tr)
	}
}

// writeBlocksError maps service sentinels onto the API's status-code
// convention (identical mapping to PUT /dependencies).
func writeBlocksError(w http.ResponseWriter, err error) {
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
}

// writeBlocksOK renders the shared success body. The blockers lookup
// failing after a successful write is a 500-through-writeError (the
// write is durable; the client re-fetches via the WS event).
func writeBlocksOK(w http.ResponseWriter, r *http.Request, deps *Dependencies, taskID string, tr *task.Task) {
	blockers, err := deps.Tasks.Blockers(r.Context(), taskID)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, blocksResponse{Blockers: blockers, Task: tr})
}
