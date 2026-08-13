// Package api — Phase 2 handlers: kanban move + column metadata updates.
package api

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/ramgml/orenda/internal/api/ws"
	"github.com/ramgml/orenda/internal/domain/project"
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

// createColumnInput is the body of POST /api/v1/projects/{id}/columns.
//
// Name is required (empty → 400). WIPLimit and Color are optional. The
// board id is looked up from the project, so the caller never sends it.
type createColumnInput struct {
	Name     string `json:"name"`
	Color    string `json:"color"`
	WIPLimit *int   `json:"wip_limit"`
}

// createColumnHandler appends a new column to the project's (single)
// board. Phase 12 made columns user-managed: previously the only way to
// add one was to insert it directly into the SQL. Position is chosen by
// the repository (max+1024) so the new column always lands at the end.
//
// 400 — empty name (or unparsable body)
// 404 — project doesn't exist (no board either)
// 422 — wip_limit would already be violated by existing tasks (rare on
//
//	create, but possible if the client supplied a small limit and
//	the next-start position already holds tasks — kept consistent
//	with patchColumnHandler's policy)
//
// 201 — the newly created column, with computed position
func createColumnHandler(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		projectID := chi.URLParam(r, "id")
		if projectID == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing_project_id"})
			return
		}
		var in createColumnInput
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_json"})
			return
		}
		if in.Name == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "name_required"})
			return
		}
		if in.WIPLimit != nil && *in.WIPLimit < 0 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "wip_limit_negative"})
			return
		}

		col := &project.Column{
			Name:     in.Name,
			Color:    in.Color,
			WIPLimit: in.WIPLimit,
		}
		created, err := deps.Projects.CreateColumn(r.Context(), projectID, col)
		if err != nil {
			writeError(w, err)
			return
		}

		// Push to the WS topic the kanban UI already subscribes to ("tasks").
		// Subscribers refetch on every event, which is cheap and consistent
		// with how task.moved / task.created propagate.
		if deps.WSHub != nil {
			deps.WSHub.Publish(r.Context(), ws.Event{
				Topic: "tasks",
				Body: map[string]any{
					"type":       "column.created",
					"project_id": projectID,
					"column":     created,
				},
			})
		}
		writeJSON(w, http.StatusCreated, created)
	}
}

// patchColumnHandler updates mutable column fields.
//
// Allowed fields: name, position, wip_limit, color. wip_limit=0 means
// "remove the limit" (stored as NULL). 422 is returned when a new
// non-zero wip_limit is below the current task count in that column.
func patchColumnHandler(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var in columnInput
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_json"})
			return
		}
		id := chi.URLParam(r, "id")
		if id == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing_id"})
			return
		}
		col, err := deps.Projects.GetColumn(r.Context(), id)
		if err != nil {
			writeError(w, err)
			return
		}

		// Apply mutable fields. Empty name is rejected (the column must
		// stay non-empty so the kanban header never goes blank).
		if in.Name != "" {
			col.Name = in.Name
		}
		if in.Position != 0 {
			col.Position = in.Position
		}
		if in.Color != "" {
			col.Color = in.Color
		}
		// WIPLimit uses a pointer to distinguish "unchanged" from "clear".
		// The JSON decoder produces nil for missing; we use a *int in the
		// input struct already (above). nil here = leave as-is; non-nil =
		// explicit clear when 0, or set when > 0.
		if in.WIPLimit != nil {
			if *in.WIPLimit < 0 {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "wip_limit_negative"})
				return
			}
			if *in.WIPLimit == 0 {
				col.WIPLimit = nil // clear
			} else {
				v := *in.WIPLimit
				col.WIPLimit = &v
			}
		}

		// Validate the new limit against the current task count.
		if col.WIPLimit != nil && deps.Tasks != nil {
			n, err := deps.Tasks.CountByColumn(r.Context(), col.ID)
			if err != nil {
				writeError(w, err)
				return
			}
			if n > *col.WIPLimit {
				writeJSON(w, http.StatusUnprocessableEntity, map[string]any{
					"error":       "wip_limit_too_small",
					"current":     n,
					"wip_limit":   *col.WIPLimit,
					"snapshot_id": col.ID,
				})
				return
			}
		}

		if err := deps.Projects.UpdateColumn(r.Context(), col); err != nil {
			writeError(w, err)
			return
		}

		// Phase 27.10: parity with create/delete — broadcast
		// column.updated on the existing "tasks" topic so a second
		// tab refetches the board and renders the new colour /
		// rename / WIP change without a manual reload. The kanban
		// already subscribes to the topic; the front-end refetches
		// on any of created / updated / deleted.
		if deps.WSHub != nil {
			deps.WSHub.Publish(r.Context(), ws.Event{
				Topic: "tasks",
				Body: map[string]any{
					"type":   "column.updated",
					"column": col,
				},
			})
		}
		writeJSON(w, http.StatusOK, col)
	}
}

// deleteColumnHandler removes a column. The handler is split out from
// the generic writeError path so it can attach the task count to the
// 422 response — the UI uses that to render "N tasks are still in
// this column, move them first".
//
// Status codes:
//
//	204 — deleted
//	404 — no such column
//	422 — column still holds tasks (ErrColumnNotEmpty)
//
// On success we broadcast column.deleted on the existing 'tasks'
// topic. The kanban already subscribes to that topic and reloads on
// every event, so a single broadcast is enough to drop the column
// from every open tab.
func deleteColumnHandler(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		if id == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing_id"})
			return
		}

		// Look up the column first so we can include its board_id in the
		// WS payload (and emit a clean 404 when missing).
		col, err := deps.Projects.GetColumn(r.Context(), id)
		if err != nil {
			writeError(w, err)
			return
		}

		err = deps.Projects.DeleteColumn(r.Context(), id)
		if err != nil {
			if errors.Is(err, project.ErrColumnNotEmpty) {
				// Re-query the count so the UI gets an up-to-date number.
				var n int
				if deps.Tasks != nil {
					if c, cerr := deps.Tasks.CountByColumn(r.Context(), id); cerr == nil {
						n = c
					}
				}
				writeJSON(w, http.StatusUnprocessableEntity, map[string]any{
					"error":   "column_not_empty",
					"current": n,
				})
				return
			}
			writeError(w, err)
			return
		}

		if deps.WSHub != nil {
			deps.WSHub.Publish(r.Context(), ws.Event{
				Topic: "tasks",
				Body: map[string]any{
					"type":      "column.deleted",
					"column_id": id,
					"board_id":  col.BoardID,
				},
			})
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
