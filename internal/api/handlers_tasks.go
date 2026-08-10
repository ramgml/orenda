// Package api — task CRUD handlers.
package api

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"

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

		if err := deps.Tasks.Create(r.Context(), tr); err != nil {
			writeError(w, err)
			return
		}
		if deps.TaskService != nil {
			deps.TaskService.MirrorSave(r.Context(), tr)
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

// listSubtasksHandler returns subtasks for a task.
func listSubtasksHandler(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		subs, err := deps.Tasks.ListSubtasks(r.Context(), chi.URLParam(r, "id"))
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"subtasks": subs})
	}
}

// addSubtaskHandler appends a subtask to a task.
func addSubtaskHandler(deps Dependencies) http.HandlerFunc {
	type subtaskInput struct {
		Title    string `json:"title"`
		Position int    `json:"position"`
	}
	return func(w http.ResponseWriter, r *http.Request) {
		var in subtaskInput
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_json"})
			return
		}
		if in.Title == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing_title"})
			return
		}
		s := &task.Subtask{
			TaskID:   chi.URLParam(r, "id"),
			Title:    in.Title,
			Position: in.Position,
		}
		if err := deps.Tasks.AddSubtask(r.Context(), s); err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, s)
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
		id, err := deps.Tasks.AddChecklist(r.Context(), chi.URLParam(r, "id"), body.Title)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, map[string]any{"id": id, "title": body.Title})
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
		id, err := deps.Tasks.AddChecklistItem(r.Context(), chi.URLParam(r, "clId"), body.Title)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, map[string]any{"id": id, "title": body.Title})
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
		if err := deps.Tasks.UpdateChecklistItem(r.Context(), chi.URLParam(r, "clId"), body.Done, body.Title); err != nil {
			writeError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func deleteChecklistItemHandler(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := deps.Tasks.DeleteChecklistItem(r.Context(), chi.URLParam(r, "clId")); err != nil {
			writeError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// updateSubtaskHandler PATCHes a subtask by id. The repo's
// UpdateSubtask takes the full Subtask, so we look the row up first
// and apply the supplied fields on top.
func updateSubtaskHandler(deps Dependencies) http.HandlerFunc {
	type in struct {
		Title    *string `json:"title"`
		Done     *bool   `json:"done"`
		Position *int    `json:"position"`
	}
	return func(w http.ResponseWriter, r *http.Request) {
		var body in
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_json"})
			return
		}
		subID := chi.URLParam(r, "subId")
		s, err := deps.Tasks.GetSubtask(r.Context(), subID)
		if err != nil {
			writeError(w, err)
			return
		}
		if body.Title != nil {
			s.Title = *body.Title
		}
		if body.Done != nil {
			s.Done = *body.Done
		}
		if body.Position != nil {
			s.Position = *body.Position
		}
		if err := deps.Tasks.UpdateSubtask(r.Context(), s); err != nil {
			writeError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// deleteSubtaskHandler removes a subtask.
func deleteSubtaskHandler(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := deps.Tasks.DeleteSubtask(r.Context(), chi.URLParam(r, "subId")); err != nil {
			writeError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
