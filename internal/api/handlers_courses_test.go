package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ramgml/orenda/internal/domain/course"
	coursesvc "github.com/ramgml/orenda/internal/service/course"
)

// fakeCourseRepo is the smallest Repository implementation that
// satisfies the surface used by submitCurriculumCore / addQuizCore /
// updateLessonContentHandlerUser. The tests that need richer
// semantics already run through internal/service/course; this stub
// exists so the handler-layer wiring is exercised independently.
type fakeCourseRepo struct {
	submitCalls int
	lessons     map[string]*course.Lesson
	quizzes     map[string]*course.Quiz
}

func newFakeCourseRepo() *fakeCourseRepo {
	return &fakeCourseRepo{
		lessons: map[string]*course.Lesson{},
		quizzes: map[string]*course.Quiz{},
	}
}

func (r *fakeCourseRepo) CreateCourse(context.Context, *course.Course) error { return nil }
func (r *fakeCourseRepo) GetCourse(_ context.Context, id string) (*course.Course, error) {
	if id == "" {
		id = "course-00000001"
	}
	return &course.Course{ID: id, Title: "T", Status: course.StatusDraft, OwnerID: "u"}, nil
}
func (r *fakeCourseRepo) GetCourseByNumber(_ context.Context, _ int) (*course.Course, error) {
	return nil, course.ErrNotFound
}
func (r *fakeCourseRepo) ListCourses(context.Context, string) ([]*course.Course, error) {
	return nil, nil
}
func (r *fakeCourseRepo) UpdateCourse(context.Context, *course.Course) error {
	return nil
}
func (r *fakeCourseRepo) UpdatePaceNotesMD(context.Context, string, string) error {
	return nil
}
func (r *fakeCourseRepo) DeleteCourse(context.Context, string) error { return nil }
func (r *fakeCourseRepo) CreateModule(context.Context, *course.Module) error {
	return nil
}
func (r *fakeCourseRepo) ListModules(context.Context, string) ([]*course.Module, error) {
	return nil, nil
}
func (r *fakeCourseRepo) CreateLesson(context.Context, *course.Lesson) error {
	return nil
}
func (r *fakeCourseRepo) ListLessons(context.Context, string) ([]*course.Lesson, error) {
	return nil, nil
}
func (r *fakeCourseRepo) ListLessonsInCourse(context.Context, string) ([]*course.Lesson, error) {
	return nil, nil
}
func (r *fakeCourseRepo) GetLesson(_ context.Context, id string) (*course.Lesson, error) {
	l, ok := r.lessons[id]
	if !ok {
		return nil, course.ErrNotFound
	}
	return l, nil
}
func (r *fakeCourseRepo) GetLessonByNumber(_ context.Context, number int) (*course.Lesson, error) {
	for _, l := range r.lessons {
		if l.Number == number {
			return l, nil
		}
	}
	return nil, course.ErrNotFound
}
func (r *fakeCourseRepo) UpdateLesson(context.Context, *course.Lesson) error { return nil }
func (r *fakeCourseRepo) VelocityStatsByCourse(_ context.Context, _ string, since time.Time) (course.VelocityStats, error) {
	return course.VelocityStats{Since: since, Window: 14 * 24 * time.Hour}, nil
}
func (r *fakeCourseRepo) UpdateLessonContent(_ context.Context, id, content string, status course.LessonStatus, taskID string) error {
	l, ok := r.lessons[id]
	if !ok {
		return course.ErrNotFound
	}
	l.ContentMD = content
	l.Status = status
	l.TaskID = taskID
	return nil
}
func (r *fakeCourseRepo) GetQuiz(_ context.Context, id string) (*course.Quiz, error) {
	q, ok := r.quizzes[id]
	if !ok {
		return nil, course.ErrNotFound
	}
	return q, nil
}
func (r *fakeCourseRepo) ModuleCourseOwner(context.Context, string) (string, error) {
	return "u", nil
}
func (r *fakeCourseRepo) CreateQuiz(_ context.Context, q *course.Quiz) error {
	if q.ID == "" {
		q.ID = "q-fake"
	}
	if q.Position <= 0 {
		q.Position = len(r.quizzes) + 1
	}
	r.quizzes[q.ID] = q
	return nil
}
func (r *fakeCourseRepo) ListQuizzesInCourse(context.Context, string) ([]*course.Quiz, error) {
	return nil, nil
}
func (r *fakeCourseRepo) Progress(context.Context, string) (course.Progress, error) {
	return course.Progress{}, nil
}
func (r *fakeCourseRepo) SubmitCurriculum(_ context.Context, _ string, modules []*course.Module, lessons []*course.Lesson, quizzes []*course.Quiz) error {
	r.submitCalls++
	for _, m := range modules {
		for _, l := range lessons {
			if l.ModuleID == m.ID {
				r.lessons[l.ID] = l
			}
		}
	}
	for _, q := range quizzes {
		r.quizzes[q.ID] = q
	}
	return nil
}

// ---- Phase 30.13 granular CRUD surface (stubs) ----

func (r *fakeCourseRepo) GetModule(context.Context, string) (*course.Module, error) {
	return nil, course.ErrNotFound
}
func (r *fakeCourseRepo) UpdateModule(context.Context, *course.Module) error { return nil }
func (r *fakeCourseRepo) DeleteModule(context.Context, string) error         { return nil }
func (r *fakeCourseRepo) DeleteLesson(context.Context, string) error         { return nil }
func (r *fakeCourseRepo) UpdateQuiz(context.Context, *course.Quiz) error     { return nil }
func (r *fakeCourseRepo) DeleteQuiz(context.Context, string) error           { return nil }
func (r *fakeCourseRepo) ApplyStructure(context.Context, string, []course.ModuleOrder) error {
	return nil
}

// newFakeCourseService exposes SubmitCurriculum through the same Service
// type the router actually wires.
func newFakeCourseService(t *testing.T) (*coursesvc.Service, *fakeCourseRepo) {
	t.Helper()
	repo := newFakeCourseRepo()
	svc := coursesvc.New(repo)
	return svc, repo
}

// driveHandler mounts h at template (e.g. "/api/v1/lessons/{id}/content")
// and runs an httptest request with idValue substituted. Use this
// instead of httptest.NewRecorder + manual chi.URLParam — the handlers
// rely on chi.URLParam and only a real chi router populates that.
func driveHandler(t *testing.T, method, template, idValue, body string, h http.HandlerFunc) *httptest.ResponseRecorder {
	t.Helper()
	r := chi.NewRouter()
	switch method {
	case http.MethodPut:
		r.Put(template, h)
	case http.MethodPost:
		r.Post(template, h)
	case http.MethodGet:
		r.Get(template, h)
	default:
		t.Fatalf("driveHandler: unsupported method %s", method)
	}
	url := strings.Replace(template, "{id}", idValue, 1)
	req := httptest.NewRequest(method, url, bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestDecodeCurriculumSwap_FillsQuizLessonIDsAndReusesIDs(t *testing.T) {
	t.Parallel()
	req := curriculumRequest{
		Modules: []curriculumModule{
			{
				ID: "m-existing", Title: "Basics", Position: 1,
				Lessons: []curriculumLesson{
					{
						ID: "l-existing", Title: "Hello", Position: 1,
						ContentMD: "Hi",
						Quizzes: []curriculumQuiz{
							{ID: "q-existing", Position: 1, QuestionMD: "?", ExpectedMD: "yes", Kind: "exact"},
							{ID: "", Position: 0, QuestionMD: "Why?", Kind: "open"},
						},
					},
				},
			},
		},
	}
	modules, lessons, quizzes := decodeCurriculumSwap(req, "course-00000001")
	require.Len(t, modules, 1)
	require.Len(t, lessons, 1)
	require.Len(t, quizzes, 2)
	assert.Equal(t, "m-existing", modules[0].ID, "module ID reused")
	assert.Equal(t, "l-existing", lessons[0].ID, "lesson ID reused")
	assert.Equal(t, course.LessonLocked, lessons[0].Status)
	assert.Equal(t, "Hi", lessons[0].ContentMD)
	assert.Equal(t, "q-existing", quizzes[0].ID)
	assert.NotEmpty(t, quizzes[1].ID)
	assert.NotEqual(t, "q-existing", quizzes[1].ID)
	for _, q := range quizzes {
		assert.Equal(t, "l-existing", q.LessonID)
	}
	assert.Equal(t, course.QuizExact, quizzes[0].Kind)
	assert.Equal(t, course.QuizOpen, quizzes[1].Kind)
}

func TestSubmitCurriculumHandlerUser_OK(t *testing.T) {
	t.Parallel()
	svc, repo := newFakeCourseService(t)
	deps := &Dependencies{CourseService: svc, Courses: repo}
	body := `{"modules":[{"id":"m1","title":"M","position":1,"lessons":[
		{"id":"lesson-00000001","title":"L","position":1,"content_md":"hi",
		 "quizzes":[{"id":"q1","position":1,"question_md":"?","expected_md":"a","kind":"exact"}]}]}]}`
	w := driveHandler(t, http.MethodPut, "/api/v1/courses/{id}/curriculum", "course-00000001", body,
		func(w http.ResponseWriter, r *http.Request) { submitCurriculumCore(w, r, deps) })
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, 1, repo.submitCalls)
	_, ok := repo.lessons["lesson-00000001"]
	assert.True(t, ok, "lesson persisted")
	_, ok = repo.quizzes["q1"]
	assert.True(t, ok, "quiz persisted")
}

func TestSubmitCurriculumHandlerUser_InvalidJSON(t *testing.T) {
	t.Parallel()
	svc, repo := newFakeCourseService(t)
	deps := &Dependencies{CourseService: svc, Courses: repo}
	w := driveHandler(t, http.MethodPut, "/api/v1/courses/{id}/curriculum", "course-00000001", "not-json",
		func(w http.ResponseWriter, r *http.Request) { submitCurriculumCore(w, r, deps) })
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestAddQuizHandler_OK(t *testing.T) {
	t.Parallel()
	svc, repo := newFakeCourseService(t)
	repo.lessons["lesson-00000001"] = &course.Lesson{ID: "lesson-00000001", ModuleID: "m1", Title: "L"}
	deps := &Dependencies{CourseService: svc, Courses: repo}
	body := `{"question_md":"What is 2+2?","expected_md":"4","kind":"exact"}`
	w := driveHandler(t, http.MethodPost, "/api/v1/lessons/{id}/quizzes", "lesson-00000001", body,
		func(w http.ResponseWriter, r *http.Request) { addQuizCore(w, r, deps) })
	assert.Equal(t, http.StatusCreated, w.Code)
	var got course.Quiz
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	assert.NotEmpty(t, got.ID)
	assert.Equal(t, 1, got.Position)
}

func TestAddQuizHandler_RejectsMissingQuestion(t *testing.T) {
	t.Parallel()
	svc, repo := newFakeCourseService(t)
	repo.lessons["lesson-00000002"] = &course.Lesson{ID: "lesson-00000002", ModuleID: "m1", Title: "L"}
	deps := &Dependencies{CourseService: svc, Courses: repo}
	w := driveHandler(t, http.MethodPost, "/api/v1/lessons/{id}/quizzes", "lesson-00000002", `{"kind":"exact"}`,
		func(w http.ResponseWriter, r *http.Request) { addQuizCore(w, r, deps) })
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestAddQuizHandler_NotFoundLesson(t *testing.T) {
	t.Parallel()
	svc, repo := newFakeCourseService(t)
	deps := &Dependencies{CourseService: svc, Courses: repo}
	w := driveHandler(t, http.MethodPost, "/api/v1/lessons/{id}/quizzes", "missing",
		`{"question_md":"?","kind":"exact"}`,
		func(w http.ResponseWriter, r *http.Request) { addQuizCore(w, r, deps) })
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestUpdateLessonContentHandlerUser_OK(t *testing.T) {
	t.Parallel()
	svc, repo := newFakeCourseService(t)
	repo.lessons["lesson-uuid-001"] = &course.Lesson{
		ID: "lesson-uuid-001", ModuleID: "m1", Title: "L",
		Status: course.LessonOpen, ContentMD: "old",
	}
	deps := &Dependencies{CourseService: svc, Courses: repo}
	w := driveHandler(t, http.MethodPut, "/api/v1/lessons/{id}/content", "lesson-uuid-001",
		`{"content_md":"new body"}`, updateLessonContentHandlerUser(deps))
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "new body", repo.lessons["lesson-uuid-001"].ContentMD)
	assert.Equal(t, course.LessonOpen, repo.lessons["lesson-uuid-001"].Status,
		"owner edit must not flip lesson status")
}

func TestUpdateLessonContentHandlerUser_EmptyRejected(t *testing.T) {
	t.Parallel()
	svc, repo := newFakeCourseService(t)
	repo.lessons["lesson-uuid-002"] = &course.Lesson{ID: "lesson-uuid-002", ModuleID: "m1", Title: "L", Status: course.LessonOpen}
	deps := &Dependencies{CourseService: svc, Courses: repo}
	w := driveHandler(t, http.MethodPut, "/api/v1/lessons/{id}/content", "lesson-uuid-002",
		`{"content_md":""}`, updateLessonContentHandlerUser(deps))
	assert.Equal(t, http.StatusBadRequest, w.Code)
}
