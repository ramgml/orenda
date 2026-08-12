package service_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ramgml/orenda/internal/domain/course"
	coursesvc "github.com/ramgml/orenda/internal/service/course"
)

// ---- Stub repo + TaskCreator ----------------------------------------------
//
// The service tests don't go through SQLite. Instead they use a small
// in-memory stub that records writes — keeps the tests fast and the
// invariants legible. We add the *minimal* surface the service
// actually touches, no more.

type stubRepo struct {
	courses      map[string]*course.Course
	modules      map[string]*course.Module
	lessons      map[string]*course.Lesson
	quizzes      map[string]*course.Quiz
	moduleOwners map[string]string // moduleID → owner_id
}

func newStubRepo() *stubRepo {
	return &stubRepo{
		courses:      map[string]*course.Course{},
		modules:      map[string]*course.Module{},
		lessons:      map[string]*course.Lesson{},
		quizzes:      map[string]*course.Quiz{},
		moduleOwners: map[string]string{},
	}
}

func (r *stubRepo) CreateCourse(ctx context.Context, c *course.Course) error {
	if c.ID == "" {
		c.ID = "c-" + c.Title
	}
	r.courses[c.ID] = c
	return nil
}
func (r *stubRepo) GetCourse(ctx context.Context, id string) (*course.Course, error) {
	c, ok := r.courses[id]
	if !ok {
		return nil, course.ErrNotFound
	}
	return c, nil
}
func (r *stubRepo) ListCourses(ctx context.Context, ownerID string) ([]*course.Course, error) {
	out := make([]*course.Course, 0)
	for _, c := range r.courses {
		if ownerID == "" || c.OwnerID == ownerID {
			out = append(out, c)
		}
	}
	return out, nil
}
func (r *stubRepo) UpdateCourse(ctx context.Context, c *course.Course) error {
	if _, ok := r.courses[c.ID]; !ok {
		return course.ErrNotFound
	}
	r.courses[c.ID] = c
	return nil
}
func (r *stubRepo) DeleteCourse(ctx context.Context, id string) error {
	delete(r.courses, id)
	return nil
}
func (r *stubRepo) CreateModule(ctx context.Context, m *course.Module) error {
	if m.ID == "" {
		m.ID = "m-" + m.Title
	}
	r.modules[m.ID] = m
	if c, ok := r.courses[m.CourseID]; ok {
		r.moduleOwners[m.ID] = c.OwnerID
	}
	return nil
}
func (r *stubRepo) ListModules(ctx context.Context, courseID string) ([]*course.Module, error) {
	out := make([]*course.Module, 0)
	for _, m := range r.modules {
		if m.CourseID == courseID {
			out = append(out, m)
		}
	}
	return out, nil
}
func (r *stubRepo) CreateLesson(ctx context.Context, l *course.Lesson) error {
	if l.ID == "" {
		l.ID = "l-" + l.Title
	}
	r.lessons[l.ID] = l
	return nil
}
func (r *stubRepo) ListLessons(ctx context.Context, moduleID string) ([]*course.Lesson, error) {
	out := make([]*course.Lesson, 0)
	for _, l := range r.lessons {
		if l.ModuleID == moduleID {
			out = append(out, l)
		}
	}
	return out, nil
}
func (r *stubRepo) ListLessonsInCourse(ctx context.Context, courseID string) ([]*course.Lesson, error) {
	out := make([]*course.Lesson, 0)
	for _, l := range r.lessons {
		for _, m := range r.modules {
			if m.ID == l.ModuleID && m.CourseID == courseID {
				out = append(out, l)
			}
		}
	}
	return out, nil
}
func (r *stubRepo) GetLesson(ctx context.Context, id string) (*course.Lesson, error) {
	l, ok := r.lessons[id]
	if !ok {
		return nil, course.ErrNotFound
	}
	return l, nil
}
func (r *stubRepo) UpdateLesson(ctx context.Context, l *course.Lesson) error {
	if _, ok := r.lessons[l.ID]; !ok {
		return course.ErrNotFound
	}
	r.lessons[l.ID] = l
	return nil
}
func (r *stubRepo) UpdateLessonContent(ctx context.Context, lessonID, contentMD string, status course.LessonStatus, taskID string) error {
	l, ok := r.lessons[lessonID]
	if !ok {
		return course.ErrNotFound
	}
	l.ContentMD = contentMD
	l.Status = status
	l.TaskID = taskID
	return nil
}
func (r *stubRepo) GetQuiz(ctx context.Context, id string) (*course.Quiz, error) {
	q, ok := r.quizzes[id]
	if !ok {
		return nil, course.ErrNotFound
	}
	return q, nil
}
func (r *stubRepo) ModuleCourseOwner(ctx context.Context, moduleID string) (string, error) {
	o, ok := r.moduleOwners[moduleID]
	if !ok {
		return "", course.ErrNotFound
	}
	return o, nil
}
func (r *stubRepo) CreateQuiz(ctx context.Context, q *course.Quiz) error {
	if q.ID == "" {
		q.ID = "q-" + q.QuestionMD
	}
	r.quizzes[q.ID] = q
	return nil
}
func (r *stubRepo) ListQuizzesInCourse(ctx context.Context, courseID string) ([]*course.Quiz, error) {
	out := make([]*course.Quiz, 0)
	for _, q := range r.quizzes {
		for _, l := range r.lessons {
			if l.ID == q.LessonID {
				for _, m := range r.modules {
					if m.ID == l.ModuleID && m.CourseID == courseID {
						out = append(out, q)
					}
				}
			}
		}
	}
	return out, nil
}
func (r *stubRepo) Progress(ctx context.Context, courseID string) (course.Progress, error) {
	var total, done int
	for _, l := range r.lessons {
		for _, m := range r.modules {
			if m.ID == l.ModuleID && m.CourseID == courseID {
				total++
				if l.Status == course.LessonDone {
					done++
				}
			}
		}
	}
	return course.Progress{LessonsTotal: total, LessonsDone: done}, nil
}
func (r *stubRepo) SubmitCurriculum(ctx context.Context, courseID string, modules []*course.Module, lessons []*course.Lesson) error {
	for _, m := range modules {
		r.modules[m.ID] = m
	}
	for _, l := range lessons {
		r.lessons[l.ID] = l
	}
	return nil
}

// stubTaskCreator records what the service asks for. Phase 27.4 uses
// this to assert the generator + review task shape.
type stubTaskCreator struct {
	genCalls []string
	revCalls []string
	genErr   error
	revErr   error
}

func (s *stubTaskCreator) CreateGeneratorTask(ctx context.Context, ownerID, courseID, title, intentMD string) (string, error) {
	s.genCalls = append(s.genCalls, courseID)
	if s.genErr != nil {
		return "", s.genErr
	}
	return "gen-task-" + courseID, nil
}
func (s *stubTaskCreator) CreateQuizReviewTask(ctx context.Context, ownerID, quizID, lessonID, answer string) (string, error) {
	s.revCalls = append(s.revCalls, answer)
	if s.revErr != nil {
		return "", s.revErr
	}
	return "rev-task-" + quizID, nil
}

// ---- Tests -----------------------------------------------------------------

func TestCreateWithIntent_SpawnsGeneratorTask(t *testing.T) {
	repo := newStubRepo()
	tasks := &stubTaskCreator{}
	svc := coursesvc.New(repo).WithTaskCreator(tasks)

	c, err := svc.CreateWithIntent(context.Background(), "u-owner", "Learn Rust", "one month, 3x/week")
	require.NoError(t, err)
	assert.Equal(t, "Learn Rust", c.Title)
	assert.Equal(t, course.StatusDraft, c.Status)
	require.NotEmpty(t, c.GeneratorTaskID, "GeneratorTaskID should be wired to the task creator's return value")
	assert.Equal(t, "gen-task-"+c.ID, c.GeneratorTaskID)
	// The task creator should be invoked once with the freshly-
	// minted course ID (which is what the agent will key off).
	assert.Equal(t, []string{c.ID}, tasks.genCalls)
}

func TestCreateWithIntent_NoTaskCreator_NoError(t *testing.T) {
	repo := newStubRepo()
	svc := coursesvc.New(repo) // no WithTaskCreator — the legacy path stays valid

	c, err := svc.CreateWithIntent(context.Background(), "u-owner", "Self study", "")
	require.NoError(t, err)
	assert.Equal(t, "", c.GeneratorTaskID, "without a task creator, GeneratorTaskID stays empty")
}

func TestMaterializeLesson_LockedFlipsToOpen(t *testing.T) {
	repo := newStubRepo()
	repo.lessons["l1"] = &course.Lesson{ID: "l1", ModuleID: "m1", Title: "Intro", Status: course.LessonLocked}
	svc := coursesvc.New(repo)

	updated, err := svc.MaterializeLesson(context.Background(), "l1", "Hello world", "task-1")
	require.NoError(t, err)
	assert.Equal(t, course.LessonOpen, updated.Status, "Phase 27.4: locked → open on first materialization")
	assert.Equal(t, "Hello world", updated.ContentMD)
	assert.Equal(t, "task-1", updated.TaskID)
}

func TestMaterializeLesson_OpenStaysOpen(t *testing.T) {
	repo := newStubRepo()
	repo.lessons["l1"] = &course.Lesson{ID: "l1", ModuleID: "m1", Title: "Intro", Status: course.LessonOpen, ContentMD: "old"}
	svc := coursesvc.New(repo)

	updated, err := svc.MaterializeLesson(context.Background(), "l1", "new content", "")
	require.NoError(t, err)
	assert.Equal(t, course.LessonOpen, updated.Status, "already-open lesson stays open after materialization")
	assert.Equal(t, "new content", updated.ContentMD)
}

func TestMaterializeLesson_DoneStaysDone(t *testing.T) {
	repo := newStubRepo()
	repo.lessons["l1"] = &course.Lesson{ID: "l1", ModuleID: "m1", Title: "Intro", Status: course.LessonDone, ContentMD: "old"}
	svc := coursesvc.New(repo)

	updated, err := svc.MaterializeLesson(context.Background(), "l1", "new content", "")
	require.NoError(t, err)
	assert.Equal(t, course.LessonDone, updated.Status, "completed lessons don't auto-reopen on re-materialization")
	assert.Equal(t, "new content", updated.ContentMD, "content update happens regardless of status")
}

func TestMaterializeLesson_EmptyContentRejected(t *testing.T) {
	repo := newStubRepo()
	repo.lessons["l1"] = &course.Lesson{ID: "l1", ModuleID: "m1", Title: "Intro", Status: course.LessonLocked}
	svc := coursesvc.New(repo)

	_, err := svc.MaterializeLesson(context.Background(), "l1", "", "")
	assert.ErrorIs(t, err, coursesvc.ErrInvalidInput)
}

func TestMaterializeLesson_NotFound(t *testing.T) {
	repo := newStubRepo()
	svc := coursesvc.New(repo)
	_, err := svc.MaterializeLesson(context.Background(), "missing", "x", "")
	assert.ErrorIs(t, err, coursesvc.ErrNotFound)
}

func TestAnswerQuiz_ExactMatch(t *testing.T) {
	repo := newStubRepo()
	repo.lessons["l1"] = &course.Lesson{ID: "l1", ModuleID: "m1", Title: "Lesson"}
	repo.quizzes["q1"] = &course.Quiz{ID: "q1", LessonID: "l1", Kind: course.QuizExact, ExpectedMD: "Paris"}
	svc := coursesvc.New(repo)

	result, err := svc.AnswerQuiz(context.Background(), "q1", course.QuizAnswer{Answer: "Paris"})
	require.NoError(t, err)
	assert.True(t, result.Correct)
}

func TestAnswerQuiz_ExactNormalisesWhitespaceAndCase(t *testing.T) {
	repo := newStubRepo()
	repo.lessons["l1"] = &course.Lesson{ID: "l1", ModuleID: "m1", Title: "L"}
	repo.quizzes["q1"] = &course.Quiz{ID: "q1", LessonID: "l1", Kind: course.QuizExact, ExpectedMD: "  hello WORLD  "}
	svc := coursesvc.New(repo)

	res, err := svc.AnswerQuiz(context.Background(), "q1", course.QuizAnswer{Answer: "Hello world"})
	require.NoError(t, err)
	assert.True(t, res.Correct, "trim/lower/collapse-whitespace should normalise both sides")
}

func TestAnswerQuiz_ExactStripsDiacritics(t *testing.T) {
	repo := newStubRepo()
	repo.lessons["l1"] = &course.Lesson{ID: "l1", ModuleID: "m1", Title: "L"}
	repo.quizzes["q1"] = &course.Quiz{ID: "q1", LessonID: "l1", Kind: course.QuizExact, ExpectedMD: "cafe"}
	svc := coursesvc.New(repo)

	res, err := svc.AnswerQuiz(context.Background(), "q1", course.QuizAnswer{Answer: "café"})
	require.NoError(t, err)
	assert.True(t, res.Correct, "café should match cafe after diacritics strip")
}

func TestAnswerQuiz_ExactWrongAnswer(t *testing.T) {
	repo := newStubRepo()
	repo.lessons["l1"] = &course.Lesson{ID: "l1", ModuleID: "m1", Title: "L"}
	repo.quizzes["q1"] = &course.Quiz{ID: "q1", LessonID: "l1", Kind: course.QuizExact, ExpectedMD: "42"}
	svc := coursesvc.New(repo)

	res, err := svc.AnswerQuiz(context.Background(), "q1", course.QuizAnswer{Answer: "43"})
	require.NoError(t, err)
	assert.False(t, res.Correct)
	assert.Contains(t, res.FeedbackMD, "42")
}

func TestAnswerQuiz_OpenSpawnsReviewTask(t *testing.T) {
	repo := newStubRepo()
	repo.lessons["l1"] = &course.Lesson{ID: "l1", ModuleID: "m1", Title: "Lesson"}
	repo.quizzes["q1"] = &course.Quiz{ID: "q1", LessonID: "l1", Kind: course.QuizOpen, QuestionMD: "Why?"}
	repo.moduleOwners["m1"] = "u-owner"
	tasks := &stubTaskCreator{}
	svc := coursesvc.New(repo).WithTaskCreator(tasks)

	res, err := svc.AnswerQuiz(context.Background(), "q1", course.QuizAnswer{Answer: "my essay"})
	require.NoError(t, err)
	assert.False(t, res.Correct, "open quizzes are always pending review")
	assert.Equal(t, "rev-task-q1", res.ReviewTaskID)
	assert.Equal(t, []string{"my essay"}, tasks.revCalls)
}

func TestAnswerQuiz_OpenWithoutTaskCreator_Errors(t *testing.T) {
	repo := newStubRepo()
	repo.lessons["l1"] = &course.Lesson{ID: "l1", ModuleID: "m1", Title: "L"}
	repo.quizzes["q1"] = &course.Quiz{ID: "q1", LessonID: "l1", Kind: course.QuizOpen}
	svc := coursesvc.New(repo) // no task creator

	_, err := svc.AnswerQuiz(context.Background(), "q1", course.QuizAnswer{Answer: "x"})
	require.Error(t, err)
}

func TestAnswerQuiz_NotFound(t *testing.T) {
	repo := newStubRepo()
	svc := coursesvc.New(repo)
	_, err := svc.AnswerQuiz(context.Background(), "missing", course.QuizAnswer{})
	assert.ErrorIs(t, err, coursesvc.ErrNotFound)
}

func TestAnswerQuiz_TaskCreatorErrorBubblesUp(t *testing.T) {
	repo := newStubRepo()
	repo.lessons["l1"] = &course.Lesson{ID: "l1", ModuleID: "m1", Title: "L"}
	repo.quizzes["q1"] = &course.Quiz{ID: "q1", LessonID: "l1", Kind: course.QuizOpen}
	repo.moduleOwners["m1"] = "u-owner"
	tasks := &stubTaskCreator{revErr: errors.New("backend down")}
	svc := coursesvc.New(repo).WithTaskCreator(tasks)

	_, err := svc.AnswerQuiz(context.Background(), "q1", course.QuizAnswer{Answer: "x"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "backend down")
}
