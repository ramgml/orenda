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
//	POST   /api/v1/lessons/{id}/quizzes/{qid}/answer
//
// Agent-side surface (RequireAgent, /api/v1/agent/*):
//
//	GET   /api/v1/agent/courses?status=draft
//	PUT   /api/v1/agent/courses/{id}/curriculum
//	POST  /api/v1/agent/lessons/{id}/materialize
//	PUT   /api/v1/agent/lessons/{id}/content
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
	// Phase 27.6: when true, the owner intends to build the
	// curriculum by hand and the service skips the agent generator
	// task. Prevents a sleeping tutor from overwriting manual work.
	SkipGenerator bool `json:"skip_generator"`
}

func listCoursesHandler(deps *Dependencies) http.HandlerFunc {
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

func createCourseHandler(deps *Dependencies) http.HandlerFunc {
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
		var opts []coursesvc.CreateOption
		if in.SkipGenerator {
			opts = append(opts, coursesvc.SkipGenerator())
		}
		c, err := deps.CourseService.CreateWithIntent(r.Context(), userID, in.Title, in.IntentMD, opts...)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, c)
	}
}

func getCourseHandler(deps *Dependencies) http.HandlerFunc {
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

func deleteCourseHandler(deps *Dependencies) http.HandlerFunc {
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

func approveCourseHandler(deps *Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Phase 29.5: shared with the agent-side activate endpoint
		// (approveCourseCore lives in handlers_agent_courses.go).
		approveCourseCore(w, r, deps)
	}
}

func requestChangesCourseHandler(deps *Dependencies) http.HandlerFunc {
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

func completeLessonHandler(deps *Dependencies) http.HandlerFunc {
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

// curriculumRequest is the body of PUT /courses/{id}/curriculum
// (user-side) and PUT /agent/courses/{id}/curriculum (agent-side).
//
// Phase 27.6: each lesson carries an optional `quizzes` array, and
// the same payload is now shared between owner and tutor. The
// shared decoder is in decodeCurriculumSwap below.
type curriculumRequest struct {
	Modules []curriculumModule `json:"modules"`
}

type curriculumModule struct {
	ID          string             `json:"id"`
	Title       string             `json:"title"`
	Description string             `json:"description,omitempty"`
	Position    int                `json:"position"`
	Lessons     []curriculumLesson `json:"lessons"`
}

type curriculumLesson struct {
	ID        string           `json:"id"`
	Title     string           `json:"title"`
	Position  int              `json:"position"`
	ContentMD string           `json:"content_md,omitempty"`
	Quizzes   []curriculumQuiz `json:"quizzes,omitempty"`
}

type curriculumQuiz struct {
	ID         string `json:"id"`
	Position   int    `json:"position"`
	QuestionMD string `json:"question_md"`
	ExpectedMD string `json:"expected_md,omitempty"`
	Kind       string `json:"kind"`
}

// decodeCurriculumSwap turns a curriculumRequest into the flat
// (modules, lessons, quizzes) shape the service expects. IDs are
// reused when present so the tutor (or the owner iterating on a
// draft) can edit without churning references. Quiz LessonIDs are
// filled in from the parent module's lesson IDs.
func decodeCurriculumSwap(req curriculumRequest, courseID string) ([]*course.Module, []*course.Lesson, []*course.Quiz) {
	var modules []*course.Module
	var lessons []*course.Lesson
	var quizzes []*course.Quiz
	for _, m := range req.Modules {
		mid := m.ID
		if mid == "" {
			mid = uuidLite()
		}
		modules = append(modules, &course.Module{
			ID:          mid,
			CourseID:    courseID,
			Title:       m.Title,
			Description: m.Description,
			Position:    m.Position,
		})
		for _, l := range m.Lessons {
			lid := l.ID
			if lid == "" {
				lid = uuidLite()
			}
			lessons = append(lessons, &course.Lesson{
				ID:        lid,
				ModuleID:  mid,
				Title:     l.Title,
				ContentMD: l.ContentMD,
				Position:  l.Position,
				Status:    course.LessonLocked,
			})
			for _, q := range l.Quizzes {
				qid := q.ID
				if qid == "" {
					qid = uuidLite()
				}
				quizzes = append(quizzes, &course.Quiz{
					ID:         qid,
					LessonID:   lid,
					Position:   q.Position,
					QuestionMD: q.QuestionMD,
					ExpectedMD: q.ExpectedMD,
					Kind:       course.QuizKind(q.Kind),
				})
			}
		}
	}
	return modules, lessons, quizzes
}

// submitCurriculumCore is the shared body of the user-side and
// agent-side submit handlers — same business logic, different
// auth middleware. Phase 27.6 promotes this from an
// agent-only endpoint to a user-facing one so the owner can build
// the curriculum themselves.
func submitCurriculumCore(w http.ResponseWriter, r *http.Request, deps *Dependencies) {
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
	modules, lessons, quizzes := decodeCurriculumSwap(req, courseID)
	if err := deps.CourseService.SubmitCurriculum(r.Context(), courseID, modules, lessons, quizzes); err != nil {
		if errors.Is(err, coursesvc.ErrTransition) {
			writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "invalid_transition"})
			return
		}
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "review"})
}

// submitCurriculumHandlerAgent is the agent-side endpoint: PUT
// replaces the course's curriculum in one tx. The submitted IDs
// (when present) are reused so the tutor can compose against an
// existing draft.
func submitCurriculumHandlerAgent(deps *Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		submitCurriculumCore(w, r, deps)
	}
}

// submitCurriculumHandlerUser is the owner-side endpoint added in
// Phase 27.6: the same atomic swap, but authenticated as the
// course's owner. This is what makes "build the curriculum by hand"
// a single round-trip.
func submitCurriculumHandlerUser(deps *Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		submitCurriculumCore(w, r, deps)
	}
}

// listCoursesHandlerAgent lists courses for the agent (tutor's
// view). Optional ?status= filter.
func listCoursesHandlerAgent(deps *Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps.Courses == nil {
			http.Error(w, "course repo not wired", http.StatusServiceUnavailable)
			return
		}
		// Single-owner: the agent sees whatever the system owner has;
		// ListCourses("") is the "list all" form. A multi-user future
		// would scope this to the agent's bound owner. The volume is
		// tiny (≤ tens of courses for a personal install), so the
		// ?status= filter below is applied in memory.
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

// ---------------------------------------------------------------------------
// Phase 27.4: lesson materialization + quiz answer endpoints.
// ---------------------------------------------------------------------------

// materializeLessonRequest is the body of POST /agent/lessons/{id}/materialize
// and PUT /agent/lessons/{id}/content (the two routes share the same shape).
type materializeLessonRequest struct {
	ContentMD string `json:"content_md"`
	TaskID    string `json:"task_id,omitempty"`
}

// materializeLessonHandlerAgent is the tutor-side endpoint: the
// agent writes the lesson body and links an exercise task. The
// service flips the lesson from locked → open.
func materializeLessonHandlerAgent(deps *Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps.CourseService == nil {
			http.Error(w, "course service not wired", http.StatusServiceUnavailable)
			return
		}
		var req materializeLessonRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_json"})
			return
		}
		if req.ContentMD == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing_content_md"})
			return
		}
		lesson, err := deps.CourseService.MaterializeLesson(
			r.Context(),
			chi.URLParam(r, "id"),
			req.ContentMD,
			req.TaskID,
		)
		if err != nil {
			if errors.Is(err, coursesvc.ErrNotFound) {
				http.Error(w, "lesson not found", http.StatusNotFound)
				return
			}
			if errors.Is(err, coursesvc.ErrInvalidInput) {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_input"})
				return
			}
			if errors.Is(err, coursesvc.ErrTransition) {
				writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "invalid_transition"})
				return
			}
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, lesson)
	}
}

// answerQuizRequest is the body of POST /lessons/{id}/quizzes/{qid}/answer.
type answerQuizRequest struct {
	Answer string `json:"answer"`
}

// answerQuizHandler submits the student's quiz answer and returns
// the graded result. For exact quizzes the function scores
// immediately; for open quizzes it spawns a review task and returns
// the task id so the UI can show "pending review".
func answerQuizHandler(deps *Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps.CourseService == nil {
			http.Error(w, "course service not wired", http.StatusServiceUnavailable)
			return
		}
		var req answerQuizRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_json"})
			return
		}
		quizID := chi.URLParam(r, "qid")
		result, err := deps.CourseService.AnswerQuiz(
			r.Context(),
			quizID,
			course.QuizAnswer{Answer: req.Answer},
		)
		if err != nil {
			if errors.Is(err, coursesvc.ErrNotFound) {
				http.Error(w, "quiz not found", http.StatusNotFound)
				return
			}
			if errors.Is(err, coursesvc.ErrInvalidInput) {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_input"})
				return
			}
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, result)
	}
}

// uuidLite is a tiny inline helper for IDs the agent service passes
// back to the repo. We don't need full UUIDv7 here — any 16-byte hex
// string is unique enough for the within-a-curriculum scope.
func uuidLite() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// ---------------------------------------------------------------------------
// Phase 27.6: user-side curriculum editor + quiz surface.
// ---------------------------------------------------------------------------

// addQuizRequest is the body of POST /lessons/{id}/quizzes (user) and
// POST /agent/lessons/{id}/quizzes (agent). Position is optional
// (0 = append at the end).
type addQuizRequest struct {
	Position   int    `json:"position"`
	QuestionMD string `json:"question_md"`
	ExpectedMD string `json:"expected_md,omitempty"`
	Kind       string `json:"kind"`
}

// addQuizCore is the shared body of the user-side and agent-side
// addQuiz handlers. The auth boundary is the route mounting.
func addQuizCore(w http.ResponseWriter, r *http.Request, deps *Dependencies) {
	if deps.CourseService == nil {
		http.Error(w, "course service not wired", http.StatusServiceUnavailable)
		return
	}
	var req addQuizRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_json"})
		return
	}
	if req.QuestionMD == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing_question_md"})
		return
	}
	if req.Kind == "" {
		req.Kind = string(course.QuizExact)
	}
	q, err := deps.CourseService.AddQuiz(
		r.Context(),
		chi.URLParam(r, "id"),
		req.QuestionMD,
		req.ExpectedMD,
		course.QuizKind(req.Kind),
	)
	if err != nil {
		switch {
		case errors.Is(err, coursesvc.ErrNotFound):
			http.Error(w, "lesson not found", http.StatusNotFound)
		case errors.Is(err, coursesvc.ErrInvalidInput):
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_input"})
		default:
			writeError(w, err)
		}
		return
	}
	// Position is set by the repo via max(position)+1 when the
	// caller passed 0; surface it in the response so the UI can
	// patch its local state.
	writeJSON(w, http.StatusCreated, q)
}

// addQuizHandler — owner-side endpoint (Phase 27.6: previously
// missing in the user namespace; quiz creation is now exposed
// under /api/v1/lessons/{id}/quizzes).
func addQuizHandler(deps *Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		addQuizCore(w, r, deps)
	}
}

// addQuizHandlerAgent — agent-side endpoint (Phase 27.6 closes
// the debt tracked since Phase 18: 18.6 promised the tutor a way
// to add a single quiz to an existing lesson without swapping the
// whole curriculum).
func addQuizHandlerAgent(deps *Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		addQuizCore(w, r, deps)
	}
}

// updateLessonContentHandlerUser — owner-side endpoint (Phase 27.6):
// the owner of an active course can edit a lesson's content_md
// directly. Agent-only MaterializeLesson still owns the
// locked → open transition; this path is for tweaks once the
// course is already live.
func updateLessonContentHandlerUser(deps *Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps.CourseService == nil {
			http.Error(w, "course service not wired", http.StatusServiceUnavailable)
			return
		}
		var req materializeLessonRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_json"})
			return
		}
		if req.ContentMD == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing_content_md"})
			return
		}
		l, err := deps.CourseService.UpdateLessonContent(
			r.Context(),
			chi.URLParam(r, "id"),
			req.ContentMD,
		)
		if err != nil {
			switch {
			case errors.Is(err, coursesvc.ErrNotFound):
				http.Error(w, "lesson not found", http.StatusNotFound)
			case errors.Is(err, coursesvc.ErrInvalidInput):
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_input"})
			default:
				writeError(w, err)
			}
			return
		}
		writeJSON(w, http.StatusOK, l)
	}
}
