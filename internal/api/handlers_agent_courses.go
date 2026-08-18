// Package api — Phase 29 agent-namespace course handlers.
//
// Phase 29 closes the asymmetry where an agent could fill a course
// (curriculum / materialize / quizzes) but never create or activate
// one. The target scenario: "agent, build me a course on X" runs
// end-to-end without a single human click.
//
//	POST /api/v1/agent/courses              create a draft course
//	POST /api/v1/agent/courses/{id}/activate   review → active
//
// Design decisions (PLAN §29):
//   - The owner of an agent-created course is the first non-system
//     user (single-owner install; the owner_id column is already
//     multi-user-ready). Not a request parameter.
//   - SkipGenerator is forced: the agent IS the generator, so no
//     "build the curriculum" generator task is spawned.
//   - Activation shares the exact service path with the user-side
//     approve endpoint (approveCourseCore) — no copied logic.
package api

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/ramgml/orenda/internal/domain/user"
	coursesvc "github.com/ramgml/orenda/internal/service/course"
)

// createCourseHandlerAgent — POST /agent/courses. The body reuses
// createCourseRequest; skip_generator is ignored (always on for the
// agent path — the caller is the generator).
func createCourseHandlerAgent(deps *Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps.CourseService == nil || deps.Users == nil {
			http.Error(w, "course service not wired", http.StatusServiceUnavailable)
			return
		}
		var in createCourseRequest
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_json"})
			return
		}
		if in.Title == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing_title"})
			return
		}
		owner, err := deps.Users.FirstNonSystem(r.Context())
		if err != nil {
			if errors.Is(err, user.ErrNotFound) {
				// No human owner yet — the operator must create one
				// before agents can file courses.
				writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "owner_not_configured"})
				return
			}
			writeError(w, err)
			return
		}
		c, err := deps.CourseService.CreateWithIntent(
			r.Context(), owner.ID, in.Title, in.IntentMD, coursesvc.SkipGenerator(),
		)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, c)
	}
}

// approveCourseCore is the user-side approve body. It runs the
// review → active transition through the service, which writes a
// course_activity row with kind=approved (Phase 32.5).
//
// The agent-side activate endpoint is structurally similar but
// records a different activity kind (activated), so it calls
// ActivateCourse directly rather than reusing this helper.
func approveCourseCore(w http.ResponseWriter, r *http.Request, deps *Dependencies) {
	if deps.CourseService == nil {
		http.Error(w, "course service not wired", http.StatusServiceUnavailable)
		return
	}
	c, err := deps.CourseService.ApproveCurriculum(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		if errors.Is(err, coursesvc.ErrTransition) {
			writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "invalid_transition"})
			return
		}
		if errors.Is(err, coursesvc.ErrNotFound) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "not_found"})
			return
		}
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, c)
}

// activateCourseHandlerAgent — POST /agent/courses/{id}/activate.
// Same transition as the owner clicking "Approve" (review → active,
// first lesson unlocked), but writes course_activity with
// kind=activated so the audit feed shows the operator which path
// was taken. Phase 32.5 pilot task #2.
func activateCourseHandlerAgent(deps *Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps.CourseService == nil {
			http.Error(w, "course service not wired", http.StatusServiceUnavailable)
			return
		}
		c, err := deps.CourseService.ActivateCourse(r.Context(), chi.URLParam(r, "id"))
		if err != nil {
			if errors.Is(err, coursesvc.ErrTransition) {
				writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "invalid_transition"})
				return
			}
			if errors.Is(err, coursesvc.ErrNotFound) {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": "not_found"})
				return
			}
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, c)
	}
}
