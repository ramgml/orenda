// Package api — agent-namespace checklists (T96).
//
// Phase 27.11 gave agents a bearer-token path for comments; T96
// extends the same pattern to task checklists so an agent can read
// the PM's «Как протестировать» QA checklist natively and tick items
// off as it works. The gating model mirrors the existing agent
// surfaces:
//
//   - READ  (GET /agent/tasks/{id}/checklists): any authenticated
//     agent — the same open-read posture as
//     /agent/tasks/{id}/context. A claimable task's checklist is
//     exactly what an agent needs before claiming.
//   - WRITE (POST checklist, POST/PATCH/DELETE items): only the
//     current lock holder. A task nobody holds (or held by another
//     agent) rejects writes with 403 not_lock_holder — the same
//     gate and error body as agent_notes in agentPatchTaskHandler.
//
// All reads/writes ride the same task.Repository surface the
// user-side handlers use — no business logic here; this file only
// adds the agent identity, the holder gate, and the agent actor on
// activity rows.
package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"

	"github.com/ramgml/orenda/internal/domain/activity"
	"github.com/ramgml/orenda/internal/domain/task"
)

// agentChecklistHolderGate enforces the lock-holder rule for
// checklist mutations. 403 not_lock_holder mirrors translateManageError's
// mapping of taskservice.ErrNotLockHolder; an identity without an
// agent id is a 401, a missing TaskService a 503 (same posture as
// the comment handler's nil-guards).
func agentChecklistHolderGate(deps *Dependencies, w http.ResponseWriter, r *http.Request, taskID string) (string, bool) {
	id, ok := IdentityFrom(r.Context())
	if !ok || id == nil || id.AgentID == "" {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return "", false
	}
	if deps.TaskService == nil {
		http.Error(w, "task service not wired", http.StatusServiceUnavailable)
		return "", false
	}
	if !deps.TaskService.IsLockHolder(r.Context(), taskID, id.AgentID) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "not_lock_holder"})
		return "", false
	}
	return id.AgentID, true
}

// checklistOnTask reports whether listID is one of taskID's
// checklists. The agent routes pair {id} with {clId} in the URL, so
// a mismatched pair is a 404 — "not found covers not yours", the
// same convention the rest of the agent namespace uses.
func checklistOnTask(ctx context.Context, deps *Dependencies, taskID, listID string) bool {
	cls, err := deps.Tasks.ListChecklists(ctx, taskID)
	if err != nil {
		return false
	}
	for _, cl := range cls {
		if cl.ID == listID {
			return true
		}
	}
	return false
}

// itemOnChecklist reports whether itemID belongs to listID. Needed
// because UpdateChecklistItem/DeleteChecklistItem are id-agnostic
// (they no-op on an unknown id), so the route-level membership
// check is what keeps a holder from touching another task's row.
func itemOnChecklist(ctx context.Context, deps *Dependencies, listID, itemID string) bool {
	its, err := deps.Tasks.ListChecklistItems(ctx, listID)
	if err != nil {
		return false
	}
	for _, it := range its {
		if it.ID == itemID {
			return true
		}
	}
	return false
}

// recordAgentChecklistActivity emits checklist activity with the
// agent as the actor — the agent-side twin of the user-side
// RecordActivity calls in handlers_tasks.go (which hardcode
// ActorUser). Nil-safe + log-on-error, same as the Phase 27.11
// comment handler.
func recordAgentChecklistActivity(deps *Dependencies, r *http.Request, taskID, agentID string, action activity.Action, payload string) {
	if deps.ActivityRecorder == nil {
		return
	}
	if err := deps.ActivityRecorder.RecordTask(r.Context(), taskID, activity.ActorAgent, agentID, action, payload); err != nil && deps.Logger != nil {
		deps.Logger.Warn("activity record failed",
			zap.String("action", string(action)),
			zap.String("task_id", taskID),
			zap.Error(err))
	}
}

// agentListChecklistsHandler — GET /api/v1/agent/tasks/{id}/checklists.
// Read is open to any authenticated agent (claimable or held).
// Response carries the checklists plus their items keyed by list id —
// the same shape as the context snapshot's checklist fields, so the
// agent parses one structure everywhere.
func agentListChecklistsHandler(deps *Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		taskID, rerr := resolveTaskRef(r.Context(), deps, chi.URLParam(r, "id"))
		if rerr != nil {
			writeResolveError(w, rerr)
			return
		}
		cls, err := deps.Tasks.ListChecklists(r.Context(), taskID)
		if err != nil {
			writeError(w, err)
			return
		}
		items := map[string][]task.ChecklistItemRow{}
		for _, cl := range cls {
			its, err := deps.Tasks.ListChecklistItems(r.Context(), cl.ID)
			if err != nil {
				writeError(w, err)
				return
			}
			items[cl.ID] = its
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"checklists":      cls,
			"checklist_items": items,
		})
	}
}

// agentAddChecklistHandler — POST /api/v1/agent/tasks/{id}/checklists.
// Holder-only. Body and response shape match the user-side
// addChecklistHandler ({title} → 201 ChecklistRow).
func agentAddChecklistHandler(deps *Dependencies) http.HandlerFunc {
	type req struct {
		Title string `json:"title"`
	}
	return func(w http.ResponseWriter, r *http.Request) {
		var in req
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_json"})
			return
		}
		if in.Title == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing_title"})
			return
		}
		taskID, rerr := resolveTaskRef(r.Context(), deps, chi.URLParam(r, "id"))
		if rerr != nil {
			writeResolveError(w, rerr)
			return
		}
		agentID, ok := agentChecklistHolderGate(deps, w, r, taskID)
		if !ok {
			return
		}
		row, err := deps.Tasks.AddChecklist(r.Context(), taskID, in.Title)
		if err != nil {
			writeError(w, err)
			return
		}
		recordAgentChecklistActivity(deps, r, taskID, agentID,
			activity.ActionChecklistAdded,
			fmt.Sprintf(`{"checklist_id":%q,"title":%q}`, row.ID, row.Title))
		writeJSON(w, http.StatusCreated, row)
	}
}

// agentAddChecklistItemHandler —
// POST /api/v1/agent/tasks/{id}/checklists/{clId}/items. Holder-only;
// the checklist must belong to the path task (404 otherwise).
func agentAddChecklistItemHandler(deps *Dependencies) http.HandlerFunc {
	type req struct {
		Title string `json:"title"`
	}
	return func(w http.ResponseWriter, r *http.Request) {
		var in req
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_json"})
			return
		}
		if in.Title == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing_title"})
			return
		}
		taskID, rerr := resolveTaskRef(r.Context(), deps, chi.URLParam(r, "id"))
		if rerr != nil {
			writeResolveError(w, rerr)
			return
		}
		agentID, ok := agentChecklistHolderGate(deps, w, r, taskID)
		if !ok {
			return
		}
		listID := chi.URLParam(r, "clId")
		if !checklistOnTask(r.Context(), deps, taskID, listID) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "not_found"})
			return
		}
		row, err := deps.Tasks.AddChecklistItem(r.Context(), listID, in.Title)
		if err != nil {
			writeError(w, err)
			return
		}
		recordAgentChecklistActivity(deps, r, taskID, agentID,
			activity.ActionChecklistItemAdded,
			fmt.Sprintf(`{"checklist_id":%q,"item_id":%q,"title":%q}`, listID, row.ID, row.Title))
		writeJSON(w, http.StatusCreated, row)
	}
}

// agentUpdateChecklistItemHandler —
// PATCH /api/v1/agent/tasks/{id}/checklists/{clId}/items/{itemId}.
// Holder-only; partial update {done?, title?} — same body shape as
// the user-side handler. Ticking done also emits
// task.checklist_item_done so the owner's timeline shows what got
// verified, not just what got written.
func agentUpdateChecklistItemHandler(deps *Dependencies) http.HandlerFunc {
	type req struct {
		Title *string `json:"title"`
		Done  *bool   `json:"done"`
	}
	return func(w http.ResponseWriter, r *http.Request) {
		var in req
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_json"})
			return
		}
		taskID, rerr := resolveTaskRef(r.Context(), deps, chi.URLParam(r, "id"))
		if rerr != nil {
			writeResolveError(w, rerr)
			return
		}
		agentID, ok := agentChecklistHolderGate(deps, w, r, taskID)
		if !ok {
			return
		}
		listID := chi.URLParam(r, "clId")
		itemID := chi.URLParam(r, "itemId")
		if !checklistOnTask(r.Context(), deps, taskID, listID) ||
			!itemOnChecklist(r.Context(), deps, listID, itemID) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "not_found"})
			return
		}
		if err := deps.Tasks.UpdateChecklistItem(r.Context(), itemID, in.Done, in.Title); err != nil {
			writeError(w, err)
			return
		}
		if in.Done != nil && *in.Done {
			recordAgentChecklistActivity(deps, r, taskID, agentID,
				activity.ActionChecklistItemDone,
				fmt.Sprintf(`{"item_id":%q,"done":true}`, itemID))
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// agentDeleteChecklistItemHandler —
// DELETE /api/v1/agent/tasks/{id}/checklists/{clId}/items/{itemId}.
// Holder-only. No activity row — deletes are noise on the timeline
// (same call the user-side handler makes).
func agentDeleteChecklistItemHandler(deps *Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		taskID, rerr := resolveTaskRef(r.Context(), deps, chi.URLParam(r, "id"))
		if rerr != nil {
			writeResolveError(w, rerr)
			return
		}
		if _, ok := agentChecklistHolderGate(deps, w, r, taskID); !ok {
			return
		}
		listID := chi.URLParam(r, "clId")
		itemID := chi.URLParam(r, "itemId")
		if !checklistOnTask(r.Context(), deps, taskID, listID) ||
			!itemOnChecklist(r.Context(), deps, listID, itemID) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "not_found"})
			return
		}
		if err := deps.Tasks.DeleteChecklistItem(r.Context(), itemID); err != nil {
			writeError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
