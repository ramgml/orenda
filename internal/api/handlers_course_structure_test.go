package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ramgml/orenda/internal/domain/course"
	coursesvc "github.com/ramgml/orenda/internal/service/course"
)

// structureFake extends fakeCourseRepo with the state the granular
// handlers (Phase 30.13) actually exercise: a configurable course
// status, a module store, and a capture of the reorder payload.
type structureFake struct {
	*fakeCourseRepo
	status         course.Status
	courseMissing  bool
	modules        map[string]*course.Module
	structureCalls [][]course.ModuleOrder
	structureErr   error
}

func newStructureFake() *structureFake {
	return &structureFake{
		fakeCourseRepo: newFakeCourseRepo(),
		status:         course.StatusDraft,
		modules:        map[string]*course.Module{},
	}
}

func (r *structureFake) GetCourse(_ context.Context, id string) (*course.Course, error) {
	if r.courseMissing {
		return nil, course.ErrNotFound
	}
	return &course.Course{ID: id, Title: "T", Status: r.status, OwnerID: "u"}, nil
}

func (r *structureFake) GetModule(_ context.Context, id string) (*course.Module, error) {
	m, ok := r.modules[id]
	if !ok {
		return nil, course.ErrNotFound
	}
	return m, nil
}

func (r *structureFake) UpdateModule(_ context.Context, m *course.Module) error {
	if _, ok := r.modules[m.ID]; !ok {
		return course.ErrNotFound
	}
	r.modules[m.ID] = m
	return nil
}

func (r *structureFake) DeleteModule(_ context.Context, id string) error {
	if _, ok := r.modules[id]; !ok {
		return course.ErrNotFound
	}
	delete(r.modules, id)
	return nil
}

func (r *structureFake) DeleteLesson(_ context.Context, id string) error {
	if _, ok := r.lessons[id]; !ok {
		return course.ErrNotFound
	}
	delete(r.lessons, id)
	return nil
}

func (r *structureFake) UpdateQuiz(_ context.Context, q *course.Quiz) error {
	if _, ok := r.quizzes[q.ID]; !ok {
		return course.ErrNotFound
	}
	r.quizzes[q.ID] = q
	return nil
}

func (r *structureFake) DeleteQuiz(_ context.Context, id string) error {
	if _, ok := r.quizzes[id]; !ok {
		return course.ErrNotFound
	}
	delete(r.quizzes, id)
	return nil
}

func (r *structureFake) ApplyStructure(_ context.Context, _ string, modules []course.ModuleOrder) error {
	r.structureCalls = append(r.structureCalls, modules)
	return r.structureErr
}

// structureDeps wires a real Service over the fake repo.
func structureDeps(f *structureFake) *Dependencies {
	return &Dependencies{CourseService: coursesvc.New(f), Courses: f}
}

// driveCourseHandler mounts h at pattern and issues a request to the
// concrete url. Unlike driveHandler it supports PATCH/DELETE and any
// number of URL params.
func driveCourseHandler(t *testing.T, method, pattern, url, body string, h http.HandlerFunc) *httptest.ResponseRecorder {
	t.Helper()
	r := chi.NewRouter()
	switch method {
	case http.MethodPost:
		r.Post(pattern, h)
	case http.MethodPut:
		r.Put(pattern, h)
	case http.MethodPatch:
		r.Patch(pattern, h)
	case http.MethodDelete:
		r.Delete(pattern, h)
	default:
		t.Fatalf("driveCourseHandler: unsupported method %s", method)
	}
	req := httptest.NewRequest(method, url, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// seedTree registers module m1 (with lesson l1 and quiz q1) so the
// lesson/quiz walk in courseOfLesson/courseOfQuiz resolves.
func seedTree(f *structureFake) {
	f.modules["m1"] = &course.Module{ID: "m1", CourseID: "c1", Title: "Basics", Position: 1}
	f.lessons["l1"] = &course.Lesson{ID: "l1", ModuleID: "m1", Title: "Hello", Status: course.LessonOpen, Position: 1}
	f.quizzes["q1"] = &course.Quiz{ID: "q1", LessonID: "l1", QuestionMD: "?", ExpectedMD: "yes", Kind: course.QuizExact, Position: 1}
}

func TestStructure_CreateModule_201(t *testing.T) {
	f := newStructureFake()
	deps := structureDeps(f)
	w := driveCourseHandler(t, http.MethodPost, "/courses/{id}/modules", "/courses/c1/modules",
		`{"title":"Advanced","description":"deep dive"}`, createModuleHandler(deps))
	require.Equal(t, http.StatusCreated, w.Code)
	var m course.Module
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &m))
	assert.Equal(t, "Advanced", m.Title)
	assert.Equal(t, "c1", m.CourseID)
}

func TestStructure_CreateModule_400_MissingTitle(t *testing.T) {
	f := newStructureFake()
	w := driveCourseHandler(t, http.MethodPost, "/courses/{id}/modules", "/courses/c1/modules",
		`{"title":"  "}`, createModuleHandler(structureDeps(f)))
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "invalid_input")
}

func TestStructure_CreateModule_404_UnknownCourse(t *testing.T) {
	f := newStructureFake()
	f.courseMissing = true
	w := driveCourseHandler(t, http.MethodPost, "/courses/{id}/modules", "/courses/c1/modules",
		`{"title":"X"}`, createModuleHandler(structureDeps(f)))
	assert.Equal(t, http.StatusNotFound, w.Code)
	assert.Contains(t, w.Body.String(), "not_found")
}

func TestStructure_CreateModule_422_FrozenCourse(t *testing.T) {
	f := newStructureFake()
	f.status = course.StatusDone
	w := driveCourseHandler(t, http.MethodPost, "/courses/{id}/modules", "/courses/c1/modules",
		`{"title":"X"}`, createModuleHandler(structureDeps(f)))
	assert.Equal(t, http.StatusUnprocessableEntity, w.Code)
	assert.Contains(t, w.Body.String(), "invalid_transition")
}

func TestStructure_UpdateModule_200(t *testing.T) {
	f := newStructureFake()
	seedTree(f)
	w := driveCourseHandler(t, http.MethodPatch, "/modules/{id}", "/modules/m1",
		`{"title":"Renamed","description":"d"}`, updateModuleHandler(structureDeps(f)))
	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "Renamed", f.modules["m1"].Title)
}

func TestStructure_UpdateModule_404(t *testing.T) {
	f := newStructureFake()
	w := driveCourseHandler(t, http.MethodPatch, "/modules/{id}", "/modules/nope",
		`{"title":"X"}`, updateModuleHandler(structureDeps(f)))
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestStructure_DeleteModule_204And404(t *testing.T) {
	f := newStructureFake()
	seedTree(f)
	deps := structureDeps(f)
	w := driveCourseHandler(t, http.MethodDelete, "/modules/{id}", "/modules/m1", "", deleteModuleHandler(deps))
	assert.Equal(t, http.StatusNoContent, w.Code)
	w = driveCourseHandler(t, http.MethodDelete, "/modules/{id}", "/modules/m1", "", deleteModuleHandler(deps))
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestStructure_CreateLesson_201_BornLocked(t *testing.T) {
	f := newStructureFake()
	seedTree(f)
	f.status = course.StatusActive
	w := driveCourseHandler(t, http.MethodPost, "/modules/{id}/lessons", "/modules/m1/lessons",
		`{"title":"Second"}`, createLessonHandler(structureDeps(f)))
	require.Equal(t, http.StatusCreated, w.Code)
	var l course.Lesson
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &l))
	assert.Equal(t, course.LessonLocked, l.Status, "new lessons must be born locked even in active courses")
	assert.Equal(t, "m1", l.ModuleID)
}

func TestStructure_RenameLesson_200_PreservesStatus(t *testing.T) {
	f := newStructureFake()
	seedTree(f)
	w := driveCourseHandler(t, http.MethodPatch, "/lessons/{id}", "/lessons/l1",
		`{"title":"Hello v2"}`, renameLessonHandler(structureDeps(f)))
	require.Equal(t, http.StatusOK, w.Code)
	var l course.Lesson
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &l))
	assert.Equal(t, "Hello v2", l.Title)
	assert.Equal(t, course.LessonOpen, l.Status, "rename must not touch lesson status (student progress)")
}

func TestStructure_RenameLesson_422_Frozen(t *testing.T) {
	f := newStructureFake()
	seedTree(f)
	f.status = course.StatusArchived
	w := driveCourseHandler(t, http.MethodPatch, "/lessons/{id}", "/lessons/l1",
		`{"title":"X"}`, renameLessonHandler(structureDeps(f)))
	assert.Equal(t, http.StatusUnprocessableEntity, w.Code)
}

func TestStructure_DeleteLesson_204And404(t *testing.T) {
	f := newStructureFake()
	seedTree(f)
	deps := structureDeps(f)
	w := driveCourseHandler(t, http.MethodDelete, "/lessons/{id}", "/lessons/l1", "", deleteLessonHandler(deps))
	assert.Equal(t, http.StatusNoContent, w.Code)
	w = driveCourseHandler(t, http.MethodDelete, "/lessons/{id}", "/lessons/l1", "", deleteLessonHandler(deps))
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestStructure_UpdateQuiz_200_DefaultsExactKind(t *testing.T) {
	f := newStructureFake()
	seedTree(f)
	w := driveCourseHandler(t, http.MethodPatch, "/quizzes/{qid}", "/quizzes/q1",
		`{"question_md":"new question","expected_md":"42"}`, updateQuizHandler(structureDeps(f)))
	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "new question", f.quizzes["q1"].QuestionMD)
	assert.Equal(t, course.QuizExact, f.quizzes["q1"].Kind, "empty kind must default to exact")
}

func TestStructure_UpdateQuiz_400(t *testing.T) {
	f := newStructureFake()
	seedTree(f)
	deps := structureDeps(f)
	w := driveCourseHandler(t, http.MethodPatch, "/quizzes/{qid}", "/quizzes/q1",
		`{"question_md":" ","kind":"exact"}`, updateQuizHandler(deps))
	assert.Equal(t, http.StatusBadRequest, w.Code)
	w = driveCourseHandler(t, http.MethodPatch, "/quizzes/{qid}", "/quizzes/q1",
		`{"question_md":"ok","kind":"bogus"}`, updateQuizHandler(deps))
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestStructure_DeleteQuiz_204And404(t *testing.T) {
	f := newStructureFake()
	seedTree(f)
	deps := structureDeps(f)
	w := driveCourseHandler(t, http.MethodDelete, "/quizzes/{qid}", "/quizzes/q1", "", deleteQuizHandler(deps))
	assert.Equal(t, http.StatusNoContent, w.Code)
	w = driveCourseHandler(t, http.MethodDelete, "/quizzes/{qid}", "/quizzes/q1", "", deleteQuizHandler(deps))
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestStructure_ApplyStructure_200_ReturnsTree(t *testing.T) {
	f := newStructureFake()
	seedTree(f)
	w := driveCourseHandler(t, http.MethodPut, "/courses/{id}/structure", "/courses/c1/structure",
		`{"modules":[{"module_id":"m1","lesson_ids":["l1"]}]}`, applyStructureHandler(structureDeps(f)))
	require.Equal(t, http.StatusOK, w.Code)
	require.Len(t, f.structureCalls, 1)
	assert.Equal(t, "m1", f.structureCalls[0][0].ModuleID)
	assert.Equal(t, []string{"l1"}, f.structureCalls[0][0].LessonIDs)
	// The response is the refreshed course tree (same shape as GET).
	assert.Contains(t, w.Body.String(), `"modules"`)
}

func TestStructure_ApplyStructure_400_InvalidCoverage(t *testing.T) {
	f := newStructureFake()
	f.structureErr = course.ErrInvalidInput
	w := driveCourseHandler(t, http.MethodPut, "/courses/{id}/structure", "/courses/c1/structure",
		`{"modules":[{"module_id":"unknown","lesson_ids":[]}]}`, applyStructureHandler(structureDeps(f)))
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "invalid_input")
}

func TestStructure_ApplyStructure_422_Frozen(t *testing.T) {
	f := newStructureFake()
	f.status = course.StatusDone
	w := driveCourseHandler(t, http.MethodPut, "/courses/{id}/structure", "/courses/c1/structure",
		`{"modules":[]}`, applyStructureHandler(structureDeps(f)))
	assert.Equal(t, http.StatusUnprocessableEntity, w.Code)
}

func TestStructure_WriteCourseSvcError_Mapping(t *testing.T) {
	cases := []struct {
		err  error
		code int
	}{
		{coursesvc.ErrNotFound, http.StatusNotFound},
		{coursesvc.ErrInvalidInput, http.StatusBadRequest},
		{coursesvc.ErrTransition, http.StatusUnprocessableEntity},
		{errors.New("boom"), http.StatusInternalServerError},
	}
	for _, tc := range cases {
		w := httptest.NewRecorder()
		writeCourseSvcError(w, tc.err)
		assert.Equal(t, tc.code, w.Code, "error %v", tc.err)
	}
}
