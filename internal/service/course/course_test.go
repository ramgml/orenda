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
	// submitCalls records every SubmitCurriculum invocation so the
	// tests can assert "the right shape went to the repo".
	submitCalls []submitCall
}

type submitCall struct {
	modules []*course.Module
	lessons []*course.Lesson
	quizzes []*course.Quiz
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

// UpdatePaceNotesMD is the narrow PATCH added in Phase 31; the stub
// forwards to the same map so service tests that exercise the
// update path see the new value through GetCourse without
// duplicating persistence logic.
func (r *stubRepo) UpdatePaceNotesMD(ctx context.Context, id, notes string) error {
	c, ok := r.courses[id]
	if !ok {
		return course.ErrNotFound
	}
	c.PaceNotesMD = notes
	r.courses[id] = c
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
func (r *stubRepo) SubmitCurriculum(ctx context.Context, courseID string, modules []*course.Module, lessons []*course.Lesson, quizzes []*course.Quiz) error {
	r.submitCalls = append(r.submitCalls, submitCall{modules: modules, lessons: lessons, quizzes: quizzes})
	for _, m := range modules {
		r.modules[m.ID] = m
	}
	for _, l := range lessons {
		r.lessons[l.ID] = l
	}
	for _, q := range quizzes {
		r.quizzes[q.ID] = q
	}
	return nil
}

// ---- Phase 30.13 granular CRUD surface ----

func (r *stubRepo) GetModule(ctx context.Context, id string) (*course.Module, error) {
	m, ok := r.modules[id]
	if !ok {
		return nil, course.ErrNotFound
	}
	return m, nil
}
func (r *stubRepo) UpdateModule(ctx context.Context, m *course.Module) error {
	cur, ok := r.modules[m.ID]
	if !ok {
		return course.ErrNotFound
	}
	cur.Title = m.Title
	cur.Description = m.Description
	return nil
}
func (r *stubRepo) DeleteModule(ctx context.Context, id string) error {
	if _, ok := r.modules[id]; !ok {
		return course.ErrNotFound
	}
	delete(r.modules, id)
	for lid, l := range r.lessons {
		if l.ModuleID == id {
			delete(r.lessons, lid)
			for qid, q := range r.quizzes {
				if q.LessonID == lid {
					delete(r.quizzes, qid)
				}
			}
		}
	}
	return nil
}
func (r *stubRepo) DeleteLesson(ctx context.Context, id string) error {
	if _, ok := r.lessons[id]; !ok {
		return course.ErrNotFound
	}
	delete(r.lessons, id)
	for qid, q := range r.quizzes {
		if q.LessonID == id {
			delete(r.quizzes, qid)
		}
	}
	return nil
}
func (r *stubRepo) UpdateQuiz(ctx context.Context, q *course.Quiz) error {
	cur, ok := r.quizzes[q.ID]
	if !ok {
		return course.ErrNotFound
	}
	cur.QuestionMD = q.QuestionMD
	cur.ExpectedMD = q.ExpectedMD
	cur.Kind = q.Kind
	return nil
}
func (r *stubRepo) DeleteQuiz(ctx context.Context, id string) error {
	if _, ok := r.quizzes[id]; !ok {
		return course.ErrNotFound
	}
	delete(r.quizzes, id)
	return nil
}
func (r *stubRepo) ApplyStructure(ctx context.Context, courseID string, modules []course.ModuleOrder) error {
	// Mirror the real repo's exact-coverage contract: every module
	// and every lesson of the course must be named exactly once.
	wantModules := map[string]struct{}{}
	wantLessons := map[string]struct{}{}
	for _, m := range r.modules {
		if m.CourseID == courseID {
			wantModules[m.ID] = struct{}{}
		}
	}
	for _, l := range r.lessons {
		if m, ok := r.modules[l.ModuleID]; ok && m.CourseID == courseID {
			wantLessons[l.ID] = struct{}{}
		}
	}
	seenM := map[string]struct{}{}
	seenL := map[string]struct{}{}
	for _, mo := range modules {
		if _, ok := wantModules[mo.ModuleID]; !ok {
			return course.ErrInvalidInput
		}
		if _, dup := seenM[mo.ModuleID]; dup {
			return course.ErrInvalidInput
		}
		seenM[mo.ModuleID] = struct{}{}
		for _, lid := range mo.LessonIDs {
			if _, ok := wantLessons[lid]; !ok {
				return course.ErrInvalidInput
			}
			if _, dup := seenL[lid]; dup {
				return course.ErrInvalidInput
			}
			seenL[lid] = struct{}{}
		}
	}
	if len(seenM) != len(wantModules) || len(seenL) != len(wantLessons) {
		return course.ErrInvalidInput
	}
	for i, mo := range modules {
		m := r.modules[mo.ModuleID]
		m.Position = i + 1
		for j, lid := range mo.LessonIDs {
			l := r.lessons[lid]
			l.ModuleID = mo.ModuleID
			l.Position = j + 1
		}
	}
	return nil
}

// stubTaskCreator records what the service asks for. Phase 27.4 uses
// this to assert the generator + review task shape. Phase 27.6
// extends it with CompleteTask so tests can verify the
// generator-task seam — the owner builds the curriculum, the
// generator task retires.
type stubTaskCreator struct {
	genCalls  []string
	revCalls  []string
	compCalls []string
	compNotes []string
	genErr    error
	revErr    error
	compErr   error
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
func (s *stubTaskCreator) CompleteTask(ctx context.Context, taskID, note string) error {
	s.compCalls = append(s.compCalls, taskID)
	s.compNotes = append(s.compNotes, note)
	return s.compErr
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

// ---- Phase 27.6: user-side curriculum swap + quiz surface -----------------

// TestSubmitCurriculum_RetiresGeneratorTaskWhenOwnerBuildsByHand pins the
// most important behaviour of 27.6: when the owner submits a curriculum
// from draft, the generator task must be retired — otherwise a sleeping
// tutor wakes up and overwrites the manual tree.
func TestSubmitCurriculum_RetiresGeneratorTaskWhenOwnerBuildsByHand(t *testing.T) {
	repo := newStubRepo()
	repo.courses["c1"] = &course.Course{
		ID:              "c1",
		Title:           "Learn Vim",
		Status:          course.StatusDraft,
		OwnerID:         "u-owner",
		GeneratorTaskID: "gen-task-c1",
	}
	tasks := &stubTaskCreator{}
	svc := coursesvc.New(repo).WithTaskCreator(tasks)

	modules := []*course.Module{{ID: "m1", CourseID: "c1", Title: "Basics", Position: 1}}
	lessons := []*course.Lesson{{ID: "l1", ModuleID: "m1", Title: "Modes", Status: course.LessonLocked}}
	quizzes := []*course.Quiz{{ID: "q1", LessonID: "l1", QuestionMD: "?", Kind: course.QuizExact, Position: 1}}

	require.NoError(t, svc.SubmitCurriculum(context.Background(), "c1", modules, lessons, quizzes))

	// Repo received the quizzes in the swap.
	require.Len(t, repo.submitCalls, 1)
	assert.Equal(t, quizzes, repo.submitCalls[0].quizzes)
	assert.Equal(t, lessons, repo.submitCalls[0].lessons)

	// Course moved to review AND the generator task was retired.
	assert.Equal(t, course.StatusReview, repo.courses["c1"].Status)
	assert.Empty(t, repo.courses["c1"].GeneratorTaskID, "GeneratorTaskID must be cleared after retirement")
	require.Len(t, tasks.compCalls, 1)
	assert.Equal(t, "gen-task-c1", tasks.compCalls[0])
	assert.Contains(t, tasks.compNotes[0], "owner")
}

// TestSubmitCurriculum_SelfTransitionReviewToReview_NoRetire — when the
// owner iterates on a tutor-built program (the course is already in
// review), the generator task is not retired again. Submitting again
// would re-fire the same generator task that already completed; this
// prevents double-fire and keeps the "owner took over" signal clean.
func TestSubmitCurriculum_SelfTransitionReviewToReview_NoRetire(t *testing.T) {
	repo := newStubRepo()
	repo.courses["c1"] = &course.Course{
		ID:              "c1",
		Title:           "Iterate",
		Status:          course.StatusReview,
		OwnerID:         "u-owner",
		GeneratorTaskID: "gen-task-c1",
	}
	tasks := &stubTaskCreator{}
	svc := coursesvc.New(repo).WithTaskCreator(tasks)

	modules := []*course.Module{{ID: "m1", CourseID: "c1", Title: "M", Position: 1}}
	lessons := []*course.Lesson{{ID: "l1", ModuleID: "m1", Title: "L", Status: course.LessonLocked}}
	require.NoError(t, svc.SubmitCurriculum(context.Background(), "c1", modules, lessons, nil))

	assert.Equal(t, course.StatusReview, repo.courses["c1"].Status, "self-transition must keep review")
	assert.Equal(t, "gen-task-c1", repo.courses["c1"].GeneratorTaskID, "no retire on iteration")
	assert.Empty(t, tasks.compCalls, "no CompleteTask call expected")
}

// TestSubmitCurriculum_DraftToReview — the tutor's original happy path:
// no generator task to retire, course moves draft → review.
func TestSubmitCurriculum_DraftToReview(t *testing.T) {
	repo := newStubRepo()
	repo.courses["c1"] = &course.Course{ID: "c1", Title: "T", Status: course.StatusDraft, OwnerID: "u-owner"}
	tasks := &stubTaskCreator{}
	svc := coursesvc.New(repo).WithTaskCreator(tasks)

	modules := []*course.Module{{ID: "m1", CourseID: "c1", Title: "M", Position: 1}}
	lessons := []*course.Lesson{{ID: "l1", ModuleID: "m1", Title: "L", Status: course.LessonLocked}}
	require.NoError(t, svc.SubmitCurriculum(context.Background(), "c1", modules, lessons, nil))

	assert.Equal(t, course.StatusReview, repo.courses["c1"].Status)
	assert.Empty(t, repo.courses["c1"].GeneratorTaskID, "tutor-built course never had a generator task")
	assert.Empty(t, tasks.compCalls)
}

// TestSubmitCurriculum_RejectsFromActive — once the course is active,
// swap is no longer allowed; the user must edit through the targeted
// PUT /lessons/{id}/content endpoint instead.
func TestSubmitCurriculum_RejectsFromActive(t *testing.T) {
	repo := newStubRepo()
	repo.courses["c1"] = &course.Course{ID: "c1", Title: "T", Status: course.StatusActive, OwnerID: "u-owner"}
	svc := coursesvc.New(repo).WithTaskCreator(&stubTaskCreator{})

	err := svc.SubmitCurriculum(context.Background(), "c1", nil, nil, nil)
	assert.ErrorIs(t, err, coursesvc.ErrTransition)
}

// TestUpdateLessonContent_OwnerEditKeepsStatus — content edits in an
// active course must not flip the lesson's status. The user can fix a
// typo without accidentally completing or locking the lesson.
func TestUpdateLessonContent_OwnerEditKeepsStatus(t *testing.T) {
	repo := newStubRepo()
	repo.lessons["l1"] = &course.Lesson{
		ID: "l1", ModuleID: "m1", Title: "L",
		Status: course.LessonOpen, ContentMD: "old",
	}
	svc := coursesvc.New(repo)

	updated, err := svc.UpdateLessonContent(context.Background(), "l1", "new body")
	require.NoError(t, err)
	assert.Equal(t, course.LessonOpen, updated.Status)
	assert.Equal(t, "new body", updated.ContentMD)
	assert.Equal(t, course.LessonOpen, repo.lessons["l1"].Status)
}

// TestUpdateLessonContent_RejectsEmpty — same rule as MaterializeLesson:
// a lesson with no content is no better than a locked lesson.
func TestUpdateLessonContent_RejectsEmpty(t *testing.T) {
	repo := newStubRepo()
	repo.lessons["l1"] = &course.Lesson{ID: "l1", ModuleID: "m1", Title: "L", Status: course.LessonOpen}
	svc := coursesvc.New(repo)

	_, err := svc.UpdateLessonContent(context.Background(), "l1", "")
	assert.ErrorIs(t, err, coursesvc.ErrInvalidInput)
}

// TestAddQuiz_AppendsToLesson — AddQuiz persists a quiz on an existing
// lesson; the repo (real SQLite path, not this stub) picks the next
// position atomically.
func TestAddQuiz_AppendsToLesson(t *testing.T) {
	repo := newStubRepo()
	repo.lessons["l1"] = &course.Lesson{ID: "l1", ModuleID: "m1", Title: "L"}
	svc := coursesvc.New(repo)

	q, err := svc.AddQuiz(context.Background(), "l1", "What's 2+2?", "4", course.QuizExact)
	require.NoError(t, err)
	assert.Equal(t, "l1", q.LessonID)
	assert.Equal(t, "What's 2+2?", q.QuestionMD)
	assert.Equal(t, course.QuizExact, q.Kind)
	// The stub creates with id = "q-" + question.
	assert.Equal(t, "q-What's 2+2?", q.ID)
}

// TestAddQuiz_RejectsUnknownKind — guards the wire shape against
// typos like "exact_typed" or "multichoice".
func TestAddQuiz_RejectsUnknownKind(t *testing.T) {
	repo := newStubRepo()
	repo.lessons["l1"] = &course.Lesson{ID: "l1", ModuleID: "m1", Title: "L"}
	svc := coursesvc.New(repo)

	_, err := svc.AddQuiz(context.Background(), "l1", "?", "4", course.QuizKind("multichoice"))
	assert.ErrorIs(t, err, coursesvc.ErrInvalidInput)
}

// TestCreateWithIntent_SkipGenerator — wizard mode "I'll build it
// myself" — no agent generator task is created, GeneratorTaskID stays
// empty, and SubmitCurriculum later has nothing to retire.
func TestCreateWithIntent_SkipGenerator(t *testing.T) {
	repo := newStubRepo()
	tasks := &stubTaskCreator{}
	svc := coursesvc.New(repo).WithTaskCreator(tasks)

	c, err := svc.CreateWithIntent(context.Background(), "u-owner", "Self study", "",
		coursesvc.SkipGenerator())
	require.NoError(t, err)
	assert.Equal(t, course.StatusDraft, c.Status)
	assert.Empty(t, c.GeneratorTaskID)
	assert.Empty(t, tasks.genCalls, "SkipGenerator must suppress the generator task")
}
