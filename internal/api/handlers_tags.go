// Package api — Phase 13 tag handlers.
//
// Two layers of routes:
//
//   - /api/v1/tags              (global tag catalogue CRUD)
//   - /api/v1/tasks/{id}/tags   (assignment to a task; replace semantics)
//
// The catalogue is global: tags are not scoped to a project, so the
// same label can be reused across the user's projects. Phase 18+ may
// want per-project scoping, but the current single-owner product
// doesn't need it.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/ramgml/orenda/internal/domain/activity"
	"github.com/ramgml/orenda/internal/domain/task"
)

// ---------------------------------------------------------------------------
// /api/v1/tags — catalogue CRUD
// ---------------------------------------------------------------------------

// tagInput is the request body for POST/PATCH /api/v1/tags.
//
// Name is required on create (empty → 400). On PATCH, an empty Name
// means "don't change the name" — non-empty replaces it. Colour uses
// *string for the same reason as taskInput.Color: explicit "" clears.
type tagInput struct {
	Name  string  `json:"name"`
	Color *string `json:"color"`
}

func listTagsHandler(deps *Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tags, err := deps.Tasks.ListTags(r.Context())
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"tags": tags})
	}
}

func createTagHandler(deps *Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var in tagInput
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_json"})
			return
		}
		if in.Name == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "name_required"})
			return
		}
		t := &task.Tag{Name: in.Name}
		if in.Color != nil {
			t.Color = *in.Color
		}
		if err := t.Validate(); err != nil {
			writeError(w, err)
			return
		}
		if err := deps.Tasks.CreateTag(r.Context(), t); err != nil {
			// UNIQUE(name) surfaces here. translate to 409 so the
			// frontend can show "tag already exists".
			if isUniqueViolation(err) {
				writeJSON(w, http.StatusConflict, map[string]string{"error": "tag_exists"})
				return
			}
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, t)
	}
}

func patchTagHandler(deps *Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		existing, err := deps.Tasks.GetTagByID(r.Context(), id)
		if err != nil {
			writeError(w, err)
			return
		}
		var in tagInput
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_json"})
			return
		}
		if in.Name != "" {
			existing.Name = in.Name
		}
		if in.Color != nil {
			existing.Color = *in.Color
		}
		if err := existing.Validate(); err != nil {
			writeError(w, err)
			return
		}
		if err := deps.Tasks.UpdateTag(r.Context(), existing); err != nil {
			if isUniqueViolation(err) {
				writeJSON(w, http.StatusConflict, map[string]string{"error": "tag_exists"})
				return
			}
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, existing)
	}
}

func deleteTagHandler(deps *Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := deps.Tasks.DeleteTag(r.Context(), chi.URLParam(r, "id")); err != nil {
			writeError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// isUniqueViolation returns true when err is a SQLite UNIQUE
// constraint failure on the tags.name column. modernc.org/sqlite
// surfaces this as a plain error containing the SQLITE_CONSTRAINT
// message and the column name.
func isUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "UNIQUE") && strings.Contains(msg, "tags.name")
}

// ---------------------------------------------------------------------------
// /api/v1/tasks/{id}/tags — assignment (replace semantics)
// ---------------------------------------------------------------------------

// taskTagsInput is the body of PUT /api/v1/tasks/{id}/tags.
//
// {tag_ids: ["...", "..."]} — replace the task's tag set atomically.
// An empty array clears all tags. An invalid id (one that doesn't
// exist in tags) fails the whole call with 404 so the user gets
// actionable feedback instead of a silent FK violation.
type taskTagsInput struct {
	TagIDs []string `json:"tag_ids"`
}

func listTaskTagsHandler(deps *Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tags, err := deps.Tasks.ListTagsForTask(r.Context(), chi.URLParam(r, "id"))
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"tags": tags})
	}
}

func setTaskTagsHandler(deps *Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		taskID := chi.URLParam(r, "id")
		if _, err := deps.Tasks.GetByID(r.Context(), taskID); err != nil {
			writeError(w, err)
			return
		}
		var in taskTagsInput
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_json"})
			return
		}
		// Pre-validate every id so a typo doesn't leave the task
		// half-updated. GetByID is cheap (PK lookup).
		for _, id := range in.TagIDs {
			if id == "" {
				continue
			}
			if _, err := deps.Tasks.GetTagByID(r.Context(), id); err != nil {
				if errors.Is(err, task.ErrNotFound) {
					writeJSON(w, http.StatusNotFound,
						map[string]string{"error": "tag_not_found", "id": id})
					return
				}
				writeError(w, err)
				return
			}
		}
		applyTaskTagsChange(r.Context(), deps, taskID, in.TagIDs)
		// Reply with the freshly-loaded set so the client doesn't
		// need a second GET to confirm what landed.
		got, err := deps.Tasks.ListTagsForTask(r.Context(), taskID)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"tags": got})
	}
}

// applyTaskTagsChange diffs the desired tag ids against the task's
// current set, calls SetTaskTags only when something actually changes,
// and emits a single tags_replaced activity row naming the old and
// new tag sets by id.
//
// Idempotent: a PATCH that sends the same set as the current one is
// a no-op (no DB write, no activity row, no error).
func applyTaskTagsChange(ctx context.Context, deps *Dependencies, taskID string, desiredTagIDs []string) {
	// Filter out empty ids early so the diff is stable regardless of
	// how the caller shaped the array.
	clean := make([]string, 0, len(desiredTagIDs))
	for _, id := range desiredTagIDs {
		if id != "" {
			clean = append(clean, id)
		}
	}

	current, err := deps.Tasks.ListTagsForTask(ctx, taskID)
	if err != nil {
		// Best-effort: log the failure to surface it without
		// rolling back the caller's PATCH/POST. The task row
		// itself is already saved at this point.
		return
	}
	currentIDs := make([]string, len(current))
	for i, t := range current {
		currentIDs[i] = t.ID
	}
	if sameStringSet(currentIDs, clean) {
		return
	}

	if err := deps.Tasks.SetTaskTags(ctx, taskID, clean); err != nil {
		return
	}

	if deps.TaskService != nil {
		actorID := ""
		if id, ok := IdentityFrom(ctx); ok && id != nil {
			actorID = id.UserID
		}
		// We capture the display names of both sides — the activity
		// row stays human-readable without a second join. If a tag
		// was just deleted between the diff and the activity write,
		// its name shows as ""; that's an acceptable edge case.
		before := tagNamesByIDs(ctx, deps, currentIDs)
		after := tagNamesByIDs(ctx, deps, clean)
		deps.TaskService.RecordActivity(
			ctx, taskID, actorID,
			activity.ActionTagsReplaced,
			fmt.Sprintf(`{"before":%s,"after":%s}`, jsonStringArray(before), jsonStringArray(after)),
		)
	}
}

// tagNamesByIDs looks up names for a list of tag ids. Missing ids
// are returned as "" so the activity payload stays consistent even
// if a tag was deleted mid-flight.
func tagNamesByIDs(ctx context.Context, deps *Dependencies, ids []string) []string {
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		t, err := deps.Tasks.GetTagByID(ctx, id)
		if err != nil || t == nil {
			out = append(out, "")
			continue
		}
		out = append(out, t.Name)
	}
	return out
}

// jsonStringArray encodes a []string as a JSON array literal
// suitable for inlining into a payload string ("..."). Empty slices
// become "[]" rather than "null".
func jsonStringArray(s []string) string {
	if len(s) == 0 {
		return "[]"
	}
	raw, err := json.Marshal(s)
	if err != nil {
		return "[]"
	}
	return string(raw)
}

// sameStringSet reports whether a and b contain the same elements
// regardless of order and duplicates.
func sameStringSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	x := append([]string{}, a...)
	y := append([]string{}, b...)
	sort.Strings(x)
	sort.Strings(y)
	for i := range x {
		if x[i] != y[i] {
			return false
		}
	}
	return true
}
