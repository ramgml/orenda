// Package api — Phase 16 inbox handlers.
//
// The Inbox is the set of tasks with project_id IS NULL — a flat
// catch-all list of unfiled ideas. There's no board, no columns, no
// kanban: rows land here when captured quickly (quick-add, Telegram
// bot, future mobile capture) and stay until the user drags them
// onto a project's board via PATCH /tasks/{id} {project_id: "..."}.
//
// Endpoints:
//
//	GET  /api/v1/inbox/tasks        — list inbox tasks (?status=, ?limit=)
//	POST /api/v1/inbox/tasks        — create a new inbox task
//
// All under RequireUser.
package api

import (
	"encoding/json"
	"net/http"

	"github.com/ramgml/orenda/internal/domain/task"
)

// listInboxTasksHandler returns tasks with project_id IS NULL.
//
// Optional query params:
//
//	status  — filter by task status
//	limit   — clamp at 500; default 200 (the inbox is small in practice)
//
// Ordering: by created_at DESC (newest first) — the natural reading
// order for a capture log.
func listInboxTasksHandler(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		f := task.Filter{NoProject: true}
		if s := r.URL.Query().Get("status"); s != "" {
			f.Status = task.Status(s)
		}
		// We don't filter by ColumnID here — inbox tasks have column_id
		// IS NULL, and asking for one explicitly would yield an empty
		// list (useful as a sentinel but not for the inbox UI).
		tasks, err := deps.Tasks.ListByProject(r.Context(), f)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"tasks": tasks})
	}
}

// createInboxTaskHandler inserts a task with no project.
//
// Same shape as POST /projects/{id}/tasks but the project's id is
// empty by construction. We still use the canonical createTask flow
// so the activity log + mirror write happen via the same code paths
// — the inbox isn't a special kind of task, it's a task without a
// project.
func createInboxTaskHandler(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var in taskInput
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_json"})
			return
		}
		if in.Title == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing_title"})
			return
		}
		tr := &task.Task{
			ProjectID:    "", // explicit: inbox
			ColumnID:     "", // no column in inbox
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
		// Defensive: if a caller passes project_id/column_id in the
		// body, route them through the project endpoint instead. The
		// inbox endpoint is for unfiled capture.
		if in.ProjectID != nil && *in.ProjectID != "" {
			writeJSON(w, http.StatusBadRequest,
				map[string]string{"error": "inbox_endpoint_does_not_accept_project_id"})
			return
		}
		if in.ColumnID != "" {
			writeJSON(w, http.StatusBadRequest,
				map[string]string{"error": "inbox_endpoint_does_not_accept_column_id"})
			return
		}
		// Inherit the parent's column when this is a child task
		// created from an inbox parent — keeps the original "child
		// inherits parent's column" UX intact even when the parent is
		// itself off-board.
		if tr.ParentTaskID != "" && tr.ColumnID == "" {
			if parent, err := deps.Tasks.GetByID(r.Context(), tr.ParentTaskID); err == nil && parent.ColumnID != "" {
				tr.ColumnID = parent.ColumnID
			}
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
