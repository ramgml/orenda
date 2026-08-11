// Package api — agent-facing endpoints.
//
// Mounted under /api/v1/agent/* and gated by RequireAgent. Agents
// authenticate via Authorization: Bearer <api-token> and the handlers
// resolve the agent id from the context (set by RequireAgent).
package api

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/ramgml/orenda/internal/api/ws"
	"github.com/ramgml/orenda/internal/domain/agent"
	"github.com/ramgml/orenda/internal/domain/task"
	taskservice "github.com/ramgml/orenda/internal/service/task"
)

// agentMeHandler returns the agent record bound to the bearer token.
func agentMeHandler(deps Dependencies) http.HandlerFunc {
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
func agentHeartbeatHandler(deps Dependencies) http.HandlerFunc {
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
func agentClaimTaskHandler(deps Dependencies) http.HandlerFunc {
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

		tr, err := deps.TaskService.Claim(r.Context(), chi.URLParam(r, "id"), id.AgentID)
		if err != nil {
			if errors.Is(err, taskservice.ErrLockTaken) {
				writeJSON(w, http.StatusConflict, map[string]string{"error": "lock_taken"})
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
func agentReleaseTaskHandler(deps Dependencies) http.HandlerFunc {
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
		tr, err := deps.TaskService.Release(r.Context(), chi.URLParam(r, "id"), id.AgentID)
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
func agentSubmitTaskHandler(deps Dependencies) http.HandlerFunc {
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
		tr, err := deps.TaskService.Submit(r.Context(), chi.URLParam(r, "id"), id.AgentID, in.Note)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, tr)
	}
}

// agentTaskContextHandler returns a context snapshot for the bearer
// agent (same shape as the user-facing /context endpoint).
func agentTaskContextHandler(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := IdentityFrom(r.Context())
		if !ok || id.AgentID == "" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		ctx := r.Context()
		taskID := chi.URLParam(r, "id")
		tr, err := deps.Tasks.GetByID(ctx, taskID)
		if err != nil {
			writeError(w, err)
			return
		}
		// Verify the agent holds the task — agents should only see context
		// for tasks they've claimed.
		if tr.AssigneeID != id.AgentID {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		out := &TaskContext{Task: tr}
		if deps.Comments != nil {
			out.Comments, _ = deps.Comments.ListByTarget(ctx, "task", taskID)
		}
		if deps.Activities != nil {
			out.Activity, _ = deps.Activities.ListByTask(ctx, taskID)
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
		writeJSON(w, http.StatusOK, out)
	}
}

// suppress unused-import warning for task/agent in this file when tests
// are minimal; keeps the dependency surface honest.
var _ = task.StatusInProgress
var _ = agent.StatusOnline
