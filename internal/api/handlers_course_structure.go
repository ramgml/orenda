// Package api — Phase 30.13: granular curriculum structure handlers.
//
// These endpoints complement the atomic curriculum swap (Phase 27.6)
// with surgical edits that preserve row IDs — and therefore student
// progress (lesson status) and task links — on an already-active
// course:
//
//	POST   /api/v1/courses/{id}/modules        add module
//	PUT    /api/v1/courses/{id}/structure      drag&drop reorder (IDs only)
//	PATCH  /api/v1/modules/{id}                rename / description
//	DELETE /api/v1/modules/{id}                delete (cascades lessons+quizzes)
//	POST   /api/v1/modules/{id}/lessons        add lesson (born locked)
//	PATCH  /api/v1/lessons/{id}                rename lesson
//	DELETE /api/v1/lessons/{id}                delete lesson (cascades quizzes)
//	PATCH  /api/v1/quizzes/{qid}               edit question/answer/kind
//	DELETE /api/v1/quizzes/{qid}               delete quiz
//
// The same handlers are mirrored under /api/v1/agent/* (tutor agents
// fix active courses too — Phase 29 made agents course authors, so
// they need the non-destructive edit path as well). None of the
// handlers read the caller identity — the auth boundary is the route
// mounting, exactly like the Phase 29.1 wiki surface.
package api

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/ramgml/orenda/internal/domain/course"
	coursesvc "github.com/ramgml/orenda/internal/service/course"
)

// writeCourseSvcError maps course-service sentinels to HTTP statuses.
// Shared by every granular-structure handler below.
func writeCourseSvcError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, coursesvc.ErrNotFound):
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not_found"})
	case errors.Is(err, coursesvc.ErrInvalidInput):
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_input"})
	case errors.Is(err, coursesvc.ErrTransition):
		// done/archived courses are frozen for structural edits.
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "invalid_transition"})
	default:
		writeError(w, err)
	}
}

// courseServiceOr503 writes the standard 503 and returns nil when the
// course service is not wired (partial-router test fixtures).
func courseServiceOr503(w http.ResponseWriter, deps *Dependencies) *coursesvc.Service {
	if deps.CourseService == nil {
		http.Error(w, "course service not wired", http.StatusServiceUnavailable)
		return nil
	}
	return deps.CourseService
}

// ---------------------------------------------------------------------------
// Modules
// ---------------------------------------------------------------------------

// moduleUpsertRequest is the body of POST /courses/{id}/modules and
// PATCH /modules/{id}.
type moduleUpsertRequest struct {
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`
}

func createModuleHandler(deps *Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		svc := courseServiceOr503(w, deps)
		if svc == nil {
			return
		}
		var req moduleUpsertRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_json"})
			return
		}
		m, err := svc.AddModule(r.Context(), chi.URLParam(r, "id"), req.Title, req.Description)
		if err != nil {
			writeCourseSvcError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, m)
	}
}

func updateModuleHandler(deps *Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		svc := courseServiceOr503(w, deps)
		if svc == nil {
			return
		}
		var req moduleUpsertRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_json"})
			return
		}
		m, err := svc.UpdateModule(r.Context(), chi.URLParam(r, "id"), req.Title, req.Description)
		if err != nil {
			writeCourseSvcError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, m)
	}
}

func deleteModuleHandler(deps *Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		svc := courseServiceOr503(w, deps)
		if svc == nil {
			return
		}
		if err := svc.DeleteModule(r.Context(), chi.URLParam(r, "id")); err != nil {
			writeCourseSvcError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// ---------------------------------------------------------------------------
// Lessons
// ---------------------------------------------------------------------------

// lessonUpsertRequest is the body of POST /modules/{id}/lessons and
// PATCH /lessons/{id} (rename). Content edits keep using the existing
// PUT /lessons/{id}/content endpoint (Phase 27.6).
type lessonUpsertRequest struct {
	Title string `json:"title"`
}

func createLessonHandler(deps *Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		svc := courseServiceOr503(w, deps)
		if svc == nil {
			return
		}
		var req lessonUpsertRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_json"})
			return
		}
		l, err := svc.AddLesson(r.Context(), chi.URLParam(r, "id"), req.Title)
		if err != nil {
			writeCourseSvcError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, l)
	}
}

func renameLessonHandler(deps *Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		svc := courseServiceOr503(w, deps)
		if svc == nil {
			return
		}
		var req lessonUpsertRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_json"})
			return
		}
		l, err := svc.RenameLesson(r.Context(), chi.URLParam(r, "id"), req.Title)
		if err != nil {
			writeCourseSvcError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, l)
	}
}

func deleteLessonHandler(deps *Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		svc := courseServiceOr503(w, deps)
		if svc == nil {
			return
		}
		if err := svc.DeleteLesson(r.Context(), chi.URLParam(r, "id")); err != nil {
			writeCourseSvcError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// ---------------------------------------------------------------------------
// Quizzes
// ---------------------------------------------------------------------------

// quizUpsertRequest is the body of PATCH /quizzes/{qid}. Empty kind
// defaults to exact, matching the addQuiz convention.
type quizUpsertRequest struct {
	QuestionMD string `json:"question_md"`
	ExpectedMD string `json:"expected_md,omitempty"`
	Kind       string `json:"kind"`
}

func updateQuizHandler(deps *Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		svc := courseServiceOr503(w, deps)
		if svc == nil {
			return
		}
		var req quizUpsertRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_json"})
			return
		}
		if req.Kind == "" {
			req.Kind = string(course.QuizExact)
		}
		q, err := svc.UpdateQuiz(r.Context(), chi.URLParam(r, "qid"), req.QuestionMD, req.ExpectedMD, course.QuizKind(req.Kind))
		if err != nil {
			writeCourseSvcError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, q)
	}
}

func deleteQuizHandler(deps *Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		svc := courseServiceOr503(w, deps)
		if svc == nil {
			return
		}
		if err := svc.DeleteQuiz(r.Context(), chi.URLParam(r, "qid")); err != nil {
			writeCourseSvcError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// ---------------------------------------------------------------------------
// Structure (drag-and-drop reorder)
// ---------------------------------------------------------------------------

// structureRequest is the body of PUT /courses/{id}/structure. The
// payload is IDs only and must cover the course exactly — every
// module once, every lesson once. Positions are rewritten 1..n in
// payload order; lessons may move across modules.
type structureRequest struct {
	Modules []course.ModuleOrder `json:"modules"`
}

func applyStructureHandler(deps *Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps.CourseService == nil || deps.Courses == nil {
			http.Error(w, "course deps not wired", http.StatusServiceUnavailable)
			return
		}
		var req structureRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_json"})
			return
		}
		courseID := chi.URLParam(r, "id")
		if err := deps.CourseService.ApplyStructure(r.Context(), courseID, req.Modules); err != nil {
			writeCourseSvcError(w, err)
			return
		}
		// Return the refreshed tree so the UI lands the reorder in
		// one round-trip (same shape as GET /courses/{id}).
		tree, err := loadCourseTree(r, deps, courseID)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, tree)
	}
}

// loadCourseTree is the shared loader behind GET /courses/{id} and
// the structure-reorder response.
func loadCourseTree(r *http.Request, deps *Dependencies, id string) (courseTreeResponse, error) {
	c, err := deps.Courses.GetCourse(r.Context(), id)
	if err != nil {
		return courseTreeResponse{}, err
	}
	modules, err := deps.Courses.ListModules(r.Context(), id)
	if err != nil {
		return courseTreeResponse{}, err
	}
	lessons, err := deps.Courses.ListLessonsInCourse(r.Context(), id)
	if err != nil {
		return courseTreeResponse{}, err
	}
	quizzes, err := deps.Courses.ListQuizzesInCourse(r.Context(), id)
	if err != nil {
		return courseTreeResponse{}, err
	}
	prog, err := deps.Courses.Progress(r.Context(), id)
	if err != nil {
		return courseTreeResponse{}, err
	}
	return courseTreeResponse{
		Course: c, Modules: modules, Lessons: lessons,
		Quizzes: quizzes, Progress: prog,
	}, nil
}
