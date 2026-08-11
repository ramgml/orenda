// Package api — task CRUD handlers.
package api

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/ramgml/orenda/internal/domain/activity"
	"github.com/ramgml/orenda/internal/domain/task"
)

// taskInput is the JSON body for create/patch operations. Pointer fields
// distinguish "absent" from "explicitly empty".
type taskInput struct {
	Title         string            `json:"title"`
	Description   string            `json:"description"`
	Status        task.Status       `json:"status"`
	Priority      task.Priority     `json:"priority"`
	AssigneeType  task.AssigneeType `json:"assignee_type"`
	AssigneeID    string            `json:"assignee_id"`
	ColumnID      string            `json:"column_id"`
	ParentTaskID  string            `json:"parent_task_id"`
	ContextMD     string            `json:"context_md"`
	AgentNotes    string            `json:"agent_notes"`
	DueAt         *string           `json:"due_at"` // RFC3339 or null
	StartedAt     *string           `json:"started_at"`
	ClaimedAt     *string           `json:"claimed_at"`
	CompletedAt   *string           `json:"completed_at"`
	TimeEstimateS *int              `json:"time_estimate_s"`
	TimeSpentS    *int              `json:"time_spent_s"`
	Position      *float64          `json:"position"`
}

// listProjectTasksHandler returns tasks for a project, optionally filtered by
// status or column via query params.
func listProjectTasksHandler(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		f := task.Filter{
			ProjectID: chi.URLParam(r, "id"),
		}
		if s := r.URL.Query().Get("status"); s != "" {
			f.Status = task.Status(s)
		}
		if c := r.URL.Query().Get("column_id"); c != "" {
			f.ColumnID = c
		}
		tasks, err := deps.Tasks.ListByProject(r.Context(), f)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"tasks": tasks})
	}
}

// createTaskHandler creates a task in a project.
func createTaskHandler(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var in taskInput
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_json"})
			return
		}
		tr := &task.Task{
			ProjectID:    chi.URLParam(r, "id"),
			ColumnID:     in.ColumnID,
			ParentTaskID: in.ParentTaskID,
			Title:        in.Title,
			Description:  in.Description,
			Status:       in.Status,
			Priority:     in.Priority,
			AssigneeType: in.AssigneeType,
			AssigneeID:   in.AssigneeID,
			ContextMD:    in.ContextMD,
			AgentNotes:   in.AgentNotes,
		}
		if in.DueAt != nil {
			tr.DueAt = parseOptionalTime(*in.DueAt)
		}
		if in.StartedAt != nil {
			tr.StartedAt = parseOptionalTime(*in.StartedAt)
		}
		if in.ClaimedAt != nil {
			tr.ClaimedAt = parseOptionalTime(*in.ClaimedAt)
		}
		if in.CompletedAt != nil {
			tr.CompletedAt = parseOptionalTime(*in.CompletedAt)
		}
		if in.TimeEstimateS != nil {
			v := *in.TimeEstimateS
			tr.TimeEstimateS = &v
		}
		if in.Position != nil {
			tr.Position = *in.Position
		}

		// Phase 14 board UX: child tasks inherit their parent's column
		// (or fall back to the first column of the project) so they
		// appear on the kanban immediately. Without this, children
		// would be created with column_id=NULL and float off-board
		// until somebody dragged them manually. The frontend already
		// knows the parent's column and passes it through, so this is
		// just a safety net for API users who don't.
		if tr.ParentTaskID != "" && tr.ColumnID == "" {
			if parent, err := deps.Tasks.GetByID(r.Context(), tr.ParentTaskID); err == nil && parent.ColumnID != "" {
				tr.ColumnID = parent.ColumnID
			} else if colID, err := deps.Tasks.FirstColumnID(r.Context(), tr.ProjectID); err == nil {
				tr.ColumnID = colID
			}
		}

		if err := deps.Tasks.Create(r.Context(), tr); err != nil {
			writeError(w, err)
			return
		}
		if deps.TaskService != nil {
			deps.TaskService.MirrorSave(r.Context(), tr)
		}
		// Phase 14: when the new task is a child of an existing parent,
		// record the creation against the parent's activity log so the
		// parent task's timeline shows the child being added.
		if tr.ParentTaskID != "" && deps.TaskService != nil {
			actorID := ""
			if id, ok := IdentityFrom(r.Context()); ok && id != nil {
				actorID = id.UserID
			}
			deps.TaskService.RecordActivity(
				r.Context(), tr.ParentTaskID, actorID,
				activity.ActionChildAdded,
				fmt.Sprintf(`{"child_id":%q,"title":%q}`, tr.ID, tr.Title),
			)
		}
		writeJSON(w, http.StatusCreated, tr)
	}
}

// getTaskHandler returns one task.
func getTaskHandler(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tr, err := deps.Tasks.GetByID(r.Context(), chi.URLParam(r, "id"))
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, tr)
	}
}

// patchTaskHandler updates mutable task fields (PATCH and PUT both work).
func patchTaskHandler(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tr, err := deps.Tasks.GetByID(r.Context(), chi.URLParam(r, "id"))
		if err != nil {
			writeError(w, err)
			return
		}
		var in taskInput
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_json"})
			return
		}
		applyTaskPatch(tr, in)
		if err := deps.Tasks.Update(r.Context(), tr); err != nil {
			writeError(w, err)
			return
		}
		if deps.TaskService != nil {
			deps.TaskService.MirrorSave(r.Context(), tr)
		}
		writeJSON(w, http.StatusOK, tr)
	}
}

// applyTaskPatch mutates tr based on in.
//
// Non-pointer string fields are only updated when non-empty so the caller
// can send a partial document (PATCH semantics). Pointer fields carry
// explicit null intent.
func applyTaskPatch(tr *task.Task, in taskInput) {
	if in.Title != "" {
		tr.Title = in.Title
	}
	if in.Description != "" {
		tr.Description = in.Description
	}
	if in.Status != "" {
		tr.Status = in.Status
	}
	if in.Priority != "" {
		tr.Priority = in.Priority
	}
	if in.ColumnID != "" {
		tr.ColumnID = in.ColumnID
	}
	if in.ParentTaskID != "" {
		tr.ParentTaskID = in.ParentTaskID
	}
	if in.ContextMD != "" {
		tr.ContextMD = in.ContextMD
	}
	if in.AgentNotes != "" {
		tr.AgentNotes = in.AgentNotes
	}
	if in.AssigneeType != "" {
		tr.AssigneeType = in.AssigneeType
	}
	if in.AssigneeID != "" {
		tr.AssigneeID = in.AssigneeID
	}
	if in.DueAt != nil {
		tr.DueAt = parseOptionalTime(*in.DueAt)
	}
	if in.StartedAt != nil {
		tr.StartedAt = parseOptionalTime(*in.StartedAt)
	}
	if in.ClaimedAt != nil {
		tr.ClaimedAt = parseOptionalTime(*in.ClaimedAt)
	}
	if in.CompletedAt != nil {
		tr.CompletedAt = parseOptionalTime(*in.CompletedAt)
	}
	if in.TimeEstimateS != nil {
		v := *in.TimeEstimateS
		tr.TimeEstimateS = &v
	}
	if in.TimeSpentS != nil {
		tr.TimeSpentS = *in.TimeSpentS
	}
	if in.Position != nil {
		tr.Position = *in.Position
	}
}

// deleteTaskHandler removes a task.
func deleteTaskHandler(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := deps.Tasks.Delete(r.Context(), chi.URLParam(r, "id")); err != nil {
			writeError(w, err)
			return
		}
		if deps.TaskService != nil {
			deps.TaskService.MirrorDelete(chi.URLParam(r, "id"))
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// listChildTasksHandler returns the direct children of a parent task
// together with a {total, done} progress snapshot used by the
// ChildTasksList UI to draw the progress bar.
//
// Phase 14 replacement for listSubtasksHandler: subtasks are now
// first-class tasks via parent_task_id, so the UI shows them as
// cards (with status / assignee / click-to-open) rather than flat
// checkboxes.
func listChildTasksHandler(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		parentID := chi.URLParam(r, "id")
		children, err := deps.Tasks.ListChildren(ctx, parentID)
		if err != nil {
			writeError(w, err)
			return
		}
		total, done, err := deps.Tasks.ChildProgress(ctx, parentID)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"tasks": children,
			"progress": map[string]int{
				"total": total,
				"done":  done,
			},
		})
	}
}

// ---------------------------------------------------------------------------
// Checklists
// ---------------------------------------------------------------------------

func listChecklistsHandler(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rows, err := deps.Tasks.ListChecklists(r.Context(), chi.URLParam(r, "id"))
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"checklists": rows})
	}
}

func addChecklistHandler(deps Dependencies) http.HandlerFunc {
	type in struct {
		Title    string `json:"title"`
		Position int    `json:"position"`
	}
	return func(w http.ResponseWriter, r *http.Request) {
		var body in
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_json"})
			return
		}
		if body.Title == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing_title"})
			return
		}
		taskID := chi.URLParam(r, "id")
		row, err := deps.Tasks.AddChecklist(r.Context(), taskID, body.Title)
		if err != nil {
			writeError(w, err)
			return
		}
		// Phase 14: log checklist creation on the parent task's activity
		// stream so the timeline is informative without polling.
		if deps.TaskService != nil {
			actorID := ""
			if id, ok := IdentityFrom(r.Context()); ok && id != nil {
				actorID = id.UserID
			}
			deps.TaskService.RecordActivity(
				r.Context(), taskID, actorID,
				activity.ActionChecklistAdded,
				fmt.Sprintf(`{"checklist_id":%q,"title":%q}`, row.ID, row.Title),
			)
		}
		writeJSON(w, http.StatusCreated, row)
	}
}

func deleteChecklistHandler(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := deps.Tasks.DeleteChecklist(r.Context(), chi.URLParam(r, "clId")); err != nil {
			writeError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func listChecklistItemsHandler(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rows, err := deps.Tasks.ListChecklistItems(r.Context(), chi.URLParam(r, "clId"))
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": rows})
	}
}

func addChecklistItemHandler(deps Dependencies) http.HandlerFunc {
	type in struct {
		Title    string `json:"title"`
		Position int    `json:"position"`
	}
	return func(w http.ResponseWriter, r *http.Request) {
		var body in
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_json"})
			return
		}
		if body.Title == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing_title"})
			return
		}
		taskID := chi.URLParam(r, "id")
		listID := chi.URLParam(r, "clId")
		row, err := deps.Tasks.AddChecklistItem(r.Context(), listID, body.Title)
		if err != nil {
			writeError(w, err)
			return
		}
		// Phase 14: emit item_added against the parent task so the
		// activity log shows checklist work without per-row polling.
		if deps.TaskService != nil {
			actorID := ""
			if id, ok := IdentityFrom(r.Context()); ok && id != nil {
				actorID = id.UserID
			}
			deps.TaskService.RecordActivity(
				r.Context(), taskID, actorID,
				activity.ActionChecklistItemAdded,
				fmt.Sprintf(`{"checklist_id":%q,"item_id":%q,"title":%q}`, listID, row.ID, row.Title),
			)
		}
		writeJSON(w, http.StatusCreated, row)
	}
}

type updateChecklistItemInput struct {
	Title *string `json:"title"`
	Done  *bool   `json:"done"`
}

func updateChecklistItemHandler(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body updateChecklistItemInput
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_json"})
			return
		}
		itemID := chi.URLParam(r, "itemId")
		taskID := chi.URLParam(r, "id")
		if err := deps.Tasks.UpdateChecklistItem(r.Context(), itemID, body.Done, body.Title); err != nil {
			writeError(w, err)
			return
		}
		// Phase 14: emit checklist_item_done only when toggling done
		// (not on title-only edits). The activity stream then doubles
		// as a lightweight "what got checked off" feed.
		if body.Done != nil && *body.Done && deps.TaskService != nil {
			actorID := ""
			if id, ok := IdentityFrom(r.Context()); ok && id != nil {
				actorID = id.UserID
			}
			deps.TaskService.RecordActivity(
				r.Context(), taskID, actorID,
				activity.ActionChecklistItemDone,
				fmt.Sprintf(`{"item_id":%q,"done":true}`, itemID),
			)
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func deleteChecklistItemHandler(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := deps.Tasks.DeleteChecklistItem(r.Context(), chi.URLParam(r, "itemId")); err != nil {
			writeError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// updateSubtaskHandler removed in Phase 14 — child tasks are full
// tasks; use PATCH /api/v1/tasks/{id} instead.

// deleteSubtaskHandler removed in Phase 14 — child tasks are full
// tasks; use DELETE /api/v1/tasks/{id} instead.
