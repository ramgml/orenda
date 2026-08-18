// Package api — course activity feed handler (Phase 32.5 pilot task #2).
//
// GET /api/v1/courses/{id}/activity returns the course_activity
// rows for a course, newest-first. Optional ?limit=N (default 50,
// max 500). Read-only. User-side only — agents don't need their
// own feed; they emit rows via the agent-side mutation endpoints.
package api

import (
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/ramgml/orenda/internal/domain/course"
)

// listCourseActivityHandler — GET /api/v1/courses/{id}/activity.
func listCourseActivityHandler(deps *Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps.CourseActivityRepo == nil {
			http.Error(w, "course activity not wired", http.StatusServiceUnavailable)
			return
		}
		courseID := chi.URLParam(r, "id")
		if courseID == "" {
			http.Error(w, "course id required", http.StatusBadRequest)
			return
		}
		limit := 50
		if raw := r.URL.Query().Get("limit"); raw != "" {
			n, err := strconv.Atoi(raw)
			if err != nil || n <= 0 {
				http.Error(w, "invalid limit", http.StatusBadRequest)
				return
			}
			if n > 500 {
				n = 500
			}
			limit = n
		}
		rows, err := deps.CourseActivityRepo.ListByCourse(r.Context(), courseID, limit)
		if err != nil {
			writeError(w, err)
			return
		}
		// Always return [] (not null) when empty — easier for clients.
		if rows == nil {
			rows = []*course.Activity{}
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"course_id": courseID,
			"limit":     limit,
			"count":     len(rows),
			"entries":   rows,
		})
	}
}
