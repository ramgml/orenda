// Package api — Phase 18 course handlers.
//
// User-side surface (RequireUser):
//
// User-side surface (RequireUser):
//
//	GET    /api/v1/courses
//	POST   /api/v1/courses              create with intent
//	GET    /api/v1/courses/{id}         full tree (modules + lessons + quizzes)
//	DELETE /api/v1/courses/{id}
//	POST   /api/v1/courses/{id}/approve     review → active
//	POST   /api/v1/courses/{id}/request-changes   review → draft
//	POST   /api/v1/lessons/{id}/complete
//
// Agent-side surface (RequireAgent, /api/v1/agent/*):
//
//	GET   /api/v1/agent/courses?status=draft
//	PUT   /api/v1/agent/courses/{id}/curriculum
//
// The agent side is intentionally narrow — the tutor's job is to
// build the curriculum, owner decides accept/reject.
package api

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/ramgml/orenda/internal/domain/course"
	coursesvc "github.com/ramgml/orenda/internal/service/course"
)

// courseTreeResponse is the wire shape for GET /courses/{id}.
type courseTreeResponse struct {
	Course   *course.Course   `json:"course"`
	Modules  []*course.Module `json:"modules"`
	Lessons  []*course.Lesson `json:"lessons"`
	Quizzes  []*course.Quiz   `json:"quizzes"`
	Progress course.Progress  `json:"progress"`
}

// createCourseRequest is the body of POST /courses.
type createCourseRequest struct {
	Title    string `json:"title"`
	IntentMD string `json:"intent_md"`
}

func listCoursesHandler(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps.Courses == nil {
			http.Error(w, "course repo not wired", http.StatusServiceUnavailable)
			return
		}
		userID := userIDFromCtx(r)
		items, err := deps.Courses.ListCourses(r.Context(), userID)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"courses": items})
	}
}

func createCourseHandler(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps.CourseService == nil {
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
		userID := userIDFromCtx(r)
		c, err := deps.CourseService.CreateWithIntent(r.Context(), userID, in.Title, in.IntentMD)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, c)
	}
}

func getCourseHandler(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps.Courses == nil {
			http.Error(w, "course repo not wired", http.StatusServiceUnavailable)
			return
		}
		id := chi.URLParam(r, "id")
		c, err := deps.Courses.GetCourse(r.Context(), id)
		if err != nil {
			writeError(w, err)
			return
		}
		modules, err := deps.Courses.ListModules(r.Context(), id)
		if err != nil {
			writeError(w, err)
			return
		}
		lessons, err := deps.Courses.ListLessonsInCourse(r.Context(), id)
		if err != nil {
			writeError(w, err)
			return
		}
		quizzes, err := deps.Courses.ListQuizzesInCourse(r.Context(), id)
		if err != nil {
			writeError(w, err)
			return
		}
		prog, err := deps.Courses.Progress(r.Context(), id)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, courseTreeResponse{
			Course: c, Modules: modules, Lessons: lessons,
			Quizzes: quizzes, Progress: prog,
		})
	}
}

func deleteCourseHandler(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps.Courses == nil {
			http.Error(w, "course repo not wired", http.StatusServiceUnavailable)
			return
		}
		if err := deps.Courses.DeleteCourse(r.Context(), chi.URLParam(r, "id")); err != nil {
			writeError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func approveCourseHandler(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
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
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, c)
	}
}

func requestChangesCourseHandler(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps.CourseService == nil {
			http.Error(w, "course service not wired", http.StatusServiceUnavailable)
			return
		}
		c, err := deps.CourseService.RequestChanges(r.Context(), chi.URLParam(r, "id"))
		if err != nil {
			if errors.Is(err, coursesvc.ErrTransition) {
				writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "invalid_transition"})
				return
			}
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, c)
	}
}

func completeLessonHandler(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps.CourseService == nil {
			http.Error(w, "course service not wired", http.StatusServiceUnavailable)
			return
		}
		l, err := deps.CourseService.CompleteLesson(r.Context(), chi.URLParam(r, "id"))
		if err != nil {
			if errors.Is(err, coursesvc.ErrTransition) {
				writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "lesson_not_open"})
				return
			}
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, l)
	}
}

// curriculumRequest is the body of PUT /agent/courses/{id}/curriculum.
type curriculumRequest struct {
	Modules []curriculumModule `json:"modules"`
}

type curriculumModule struct {
	ID       string             `json:"id"`
	Title    string             `json:"title"`
	Position int                `json:"position"`
	Lessons  []curriculumLesson `json:"lessons"`
}

type curriculumLesson struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Position int    `json:"position"`
}

// submitCurriculumHandler is the agent-side endpoint: PUT replaces
// the course's curriculum in one tx. The submitted IDs (when present)
// are reused so the tutor can compose against an existing draft.
func submitCurriculumHandlerAgent(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps.CourseService == nil || deps.Courses == nil {
			http.Error(w, "course deps not wired", http.StatusServiceUnavailable)
			return
		}
		var req curriculumRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_json"})
			return
		}
		courseID := chi.URLParam(r, "id")
		var modules []*course.Module
		var lessons []*course.Lesson
		for _, m := range req.Modules {
			mid := m.ID
			if mid == "" {
				mid = uuidLite()
			}
			modules = append(modules, &course.Module{
				ID:       mid,
				CourseID: courseID,
				Title:    m.Title,
				Position: m.Position,
			})
			for _, l := range m.Lessons {
				lid := l.ID
				if lid == "" {
					lid = uuidLite()
				}
				lessons = append(lessons, &course.Lesson{
					ID:       lid,
					ModuleID: mid,
					Title:    l.Title,
					Position: l.Position,
					Status:   course.LessonLocked,
				})
			}
		}
		if err := deps.CourseService.SubmitCurriculum(r.Context(), courseID, modules, lessons); err != nil {
			if errors.Is(err, coursesvc.ErrTransition) {
				writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "invalid_transition"})
				return
			}
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "review"})
	}
}

// listCoursesHandlerAgent lists courses for the agent (tutor's
// view). Optional ?status= filter.
func listCoursesHandlerAgent(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps.Courses == nil {
			http.Error(w, "course repo not wired", http.StatusServiceUnavailable)
			return
		}
		// Single-owner: list everything then filter by status. The
		// volume is tiny (≤ tens of courses for a personal install).
		ownerID := ""
		// We pass an empty ownerID to ListCourses and let the repo
		// decide the scope. SQLite task repo's "list all" mode
		// isn't exposed here, so for the agent surface we use the
		// query string filter approach.
		_ = ownerID
		// Use a simple iteration: ListCourses for the bootstrap owner
		// is owner-scoped; for the agent we accept that the agent
		// sees whatever the system owner has. In a multi-user
		// future this would scope to the agent's bound owner.
		// For Phase 18 we expose a "list all" via the underlying
		// SQL — duplication avoided by going through a small
		// helper.
		items, err := deps.Courses.ListCourses(r.Context(), "")
		if err != nil {
			writeError(w, err)
			return
		}
		// Apply ?status filter.
		if s := r.URL.Query().Get("status"); s != "" {
			filtered := items[:0]
			for _, c := range items {
				if string(c.Status) == s {
					filtered = append(filtered, c)
				}
			}
			items = filtered
		}
		writeJSON(w, http.StatusOK, map[string]any{"courses": items})
	}
}

// userIDFromCtx returns the session user id (empty if anonymous).
func userIDFromCtx(r *http.Request) string {
	if id, ok := IdentityFrom(r.Context()); ok {
		return id.UserID
	}
	return ""
}

// uuidLite is a tiny inline helper for IDs the agent service passes
// back to the repo. We don't need full UUIDv7 here — any 16-byte hex
// string is unique enough for the within-a-curriculum scope.
func uuidLite() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
