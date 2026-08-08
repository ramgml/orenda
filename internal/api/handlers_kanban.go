// Package api — Phase 2 handlers: kanban move + column metadata updates.
package api

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/ramgml/orenda/internal/service/task"
)

// moveInput is the JSON body of POST /api/v1/tasks/:id/move.
//
// Exactly one of (position, before_task_id, after_task_id) should be set.
// If none are set, the task is appended to the target column.
type moveInput struct {
	ColumnID     string  `json:"column_id"`
	Position     float64 `json:"position"`
	BeforeTaskID string  `json:"before_task_id"`
	AfterTaskID  string  `json:"after_task_id"`
}

// moveTaskHandler calls Service.Move with the request body.
//
// 422 is returned for ErrColumnFull; 400 for ErrInvalidInput; 404 for
// ErrNotFound; 500 for everything else.
func moveTaskHandler(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var in moveInput
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_json"})
			return
		}
		if in.ColumnID == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing_column_id"})
			return
		}

		opts := task.MoveOptions{TargetColumnID: in.ColumnID, Position: in.Position}

		if in.BeforeTaskID != "" {
			t, err := deps.Tasks.GetByID(r.Context(), in.BeforeTaskID)
			if err != nil {
				writeError(w, err)
				return
			}
			opts.Before = t
		}
		if in.AfterTaskID != "" {
			t, err := deps.Tasks.GetByID(r.Context(), in.AfterTaskID)
			if err != nil {
				writeError(w, err)
				return
			}
			opts.After = t
		}

		tr, err := deps.TaskService.Move(r.Context(), chi.URLParam(r, "id"), opts)
		if err != nil {
			switch {
			case err == task.ErrColumnFull:
				writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "wip_limit"})
			case err == task.ErrInvalidInput:
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_input"})
			case err == task.ErrNotFound:
				writeJSON(w, http.StatusNotFound, map[string]string{"error": "not_found"})
			default:
				writeError(w, err)
			}
			return
		}
		writeJSON(w, http.StatusOK, tr)
	}
}

// columnInput is the body of PATCH /api/v1/columns/:id.
type columnInput struct {
	Name     string  `json:"name"`
	Position float64 `json:"position"`
	WIPLimit *int    `json:"wip_limit"`
	Color    string  `json:"color"`
}

// patchColumnHandler updates mutable column fields.
//
// Phase 2.3 keeps the surface tiny: name, position, wip_limit, color. The
// task_repository moves happen via moveTaskHandler above.
func patchColumnHandler(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var in columnInput
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_json"})
			return
		}
		_ = in // fields land via a Phase 2.8 endpoint that updates columns directly.
		writeJSON(w, http.StatusNotImplemented, map[string]string{
			"error": "patch_column: not implemented yet (Phase 2.8)",
		})
	}
}
