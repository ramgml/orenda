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
