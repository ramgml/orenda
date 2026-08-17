package service_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ramgml/orenda/internal/domain/course"
	coursesvc "github.com/ramgml/orenda/internal/service/course"
)

// ---- Phase 30.13: granular curriculum CRUD --------------------------
//
// The invariant under test: structural edits on an ACTIVE course must
// succeed without resetting lesson status (student progress), while
// done/archived courses stay frozen.

// seedGranular builds an active course with two modules and three
// lessons (l1 done, l2 open, l3 locked) through the stub repo.
func seedGranular(t *testing.T) (*coursesvc.Service, *stubRepo, *course.Course) {
	t.Helper()
	repo := newStubRepo()
	svc := coursesvc.New(repo)
	ctx := context.Background()
	c := &course.Course{
		ID: "c1", Title: "Learn X", Status: course.StatusActive, OwnerID: "u1",
	}
	require.NoError(t, repo.CreateCourse(ctx, c))
	require.NoError(t, repo.CreateModule(ctx, &course.Module{ID: "m1", CourseID: c.ID, Title: "Basics", Position: 1}))
	require.NoError(t, repo.CreateModule(ctx, &course.Module{ID: "m2", CourseID: c.ID, Title: "Advanced", Position: 2}))
	require.NoError(t, repo.CreateLesson(ctx, &course.Lesson{ID: "l1", ModuleID: "m1", Title: "A", Position: 1, Status: course.LessonDone}))
	require.NoError(t, repo.CreateLesson(ctx, &course.Lesson{ID: "l2", ModuleID: "m1", Title: "B", Position: 2, Status: course.LessonOpen}))
	require.NoError(t, repo.CreateLesson(ctx, &course.Lesson{ID: "l3", ModuleID: "m2", Title: "C", Position: 1, Status: course.LessonLocked}))
	require.NoError(t, repo.CreateQuiz(ctx, &course.Quiz{ID: "q1", LessonID: "l1", Position: 1, QuestionMD: "Q?", ExpectedMD: "A", Kind: course.QuizExact}))
	return svc, repo, c
}

func TestService_AddModule_AppendsAtEnd(t *testing.T) {
	svc, _, c := seedGranular(t)
	m, err := svc.AddModule(context.Background(), c.ID, "Capstone", "final project")
	require.NoError(t, err)
	assert.Equal(t, "Capstone", m.Title)
	assert.Equal(t, 3, m.Position, "third module appends at position 3")
	assert.NotEmpty(t, m.ID)
}

func TestService_AddModule_Gates(t *testing.T) {
	svc, repo, c := seedGranular(t)
	ctx := context.Background()

	// Empty title rejected.
	_, err := svc.AddModule(ctx, c.ID, "  ", "")
	assert.ErrorIs(t, err, coursesvc.ErrInvalidInput)

	// Unknown course → not found.
	_, err = svc.AddModule(ctx, "ghost", "X", "")
	assert.ErrorIs(t, err, coursesvc.ErrNotFound)

	// Done / archived courses are frozen.
	c.Status = course.StatusDone
	_, err = svc.AddModule(ctx, c.ID, "X", "")
	assert.ErrorIs(t, err, coursesvc.ErrTransition)
	c.Status = course.StatusArchived
	_, err = svc.AddModule(ctx, c.ID, "X", "")
	assert.ErrorIs(t, err, coursesvc.ErrTransition)

	// Draft and review remain editable.
	c.Status = course.StatusDraft
	_, err = svc.AddModule(ctx, c.ID, "X", "")
	require.NoError(t, err)
	c.Status = course.StatusReview
	_, err = svc.AddModule(ctx, c.ID, "Y", "")
	require.NoError(t, err)
	_ = repo
}

func TestService_UpdateModule_RenamesInPlace(t *testing.T) {
	svc, repo, _ := seedGranular(t)
	m, err := svc.UpdateModule(context.Background(), "m1", "Fundamentals", "start here")
	require.NoError(t, err)
	assert.Equal(t, "Fundamentals", m.Title)
	assert.Equal(t, "start here", m.Description)
	assert.Equal(t, 1, m.Position, "rename must not touch position")
	assert.Equal(t, "Fundamentals", repo.modules["m1"].Title)

	_, err = svc.UpdateModule(context.Background(), "ghost", "X", "")
	assert.ErrorIs(t, err, coursesvc.ErrNotFound)
	_, err = svc.UpdateModule(context.Background(), "m1", "", "")
	assert.ErrorIs(t, err, coursesvc.ErrInvalidInput)
}

func TestService_DeleteModule_CascadesAndGates(t *testing.T) {
	svc, repo, c := seedGranular(t)
	ctx := context.Background()

	require.NoError(t, svc.DeleteModule(ctx, "m1"))
	assert.NotContains(t, repo.modules, "m1")
	assert.NotContains(t, repo.lessons, "l1", "lessons cascade with module")
	assert.NotContains(t, repo.lessons, "l2")
	assert.NotContains(t, repo.quizzes, "q1", "quizzes cascade with lessons")

	assert.ErrorIs(t, svc.DeleteModule(ctx, "m1"), coursesvc.ErrNotFound)

	c.Status = course.StatusDone
	assert.ErrorIs(t, svc.DeleteModule(ctx, "m2"), coursesvc.ErrTransition)
}

func TestService_AddLesson_BornLockedAppended(t *testing.T) {
	svc, repo, _ := seedGranular(t)
	l, err := svc.AddLesson(context.Background(), "m1", "New topic")
	require.NoError(t, err)
	assert.Equal(t, course.LessonLocked, l.Status, "new lessons are born locked even in an active course")
	assert.Equal(t, 3, l.Position, "appends after the two existing m1 lessons")
	assert.Equal(t, "m1", repo.lessons[l.ID].ModuleID)

	_, err = svc.AddLesson(context.Background(), "m1", " ")
	assert.ErrorIs(t, err, coursesvc.ErrInvalidInput)
	_, err = svc.AddLesson(context.Background(), "ghost", "X")
	assert.ErrorIs(t, err, coursesvc.ErrNotFound)
}

// TestService_RenameLesson_PreservesProgress pins the core Phase 30.13
// invariant: a structural edit must not reset the lesson lifecycle.
func TestService_RenameLesson_PreservesProgress(t *testing.T) {
	svc, repo, _ := seedGranular(t)
	l, err := svc.RenameLesson(context.Background(), "l1", "A (revised)")
	require.NoError(t, err)
	assert.Equal(t, "A (revised)", l.Title)
	assert.Equal(t, course.LessonDone, repo.lessons["l1"].Status, "done stays done — progress preserved")
	assert.Equal(t, 1, repo.lessons["l1"].Position)

	assert.ErrorIs(t, svc.DeleteLesson(context.Background(), "ghost"), coursesvc.ErrNotFound)
	require.NoError(t, svc.DeleteLesson(context.Background(), "l3"))
	assert.NotContains(t, repo.lessons, "l3")
}

func TestService_UpdateQuiz_ValidatesAndPersists(t *testing.T) {
	svc, repo, _ := seedGranular(t)
	ctx := context.Background()

	q, err := svc.UpdateQuiz(ctx, "q1", "Q2?", "B", course.QuizOpen)
	require.NoError(t, err)
	assert.Equal(t, "Q2?", q.QuestionMD)
	assert.Equal(t, course.QuizOpen, repo.quizzes["q1"].Kind)

	_, err = svc.UpdateQuiz(ctx, "q1", "Q", "", "multiple_choice")
	assert.ErrorIs(t, err, coursesvc.ErrInvalidInput, "unknown kind rejected")
	_, err = svc.UpdateQuiz(ctx, "q1", "  ", "", course.QuizExact)
	assert.ErrorIs(t, err, coursesvc.ErrInvalidInput, "empty question rejected")
	_, err = svc.UpdateQuiz(ctx, "ghost", "Q", "", course.QuizExact)
	assert.ErrorIs(t, err, coursesvc.ErrNotFound)

	require.NoError(t, svc.DeleteQuiz(ctx, "q1"))
	assert.NotContains(t, repo.quizzes, "q1")
	assert.ErrorIs(t, svc.DeleteQuiz(ctx, "q1"), coursesvc.ErrNotFound)
}

// TestService_ApplyStructure_ReorderPreservesProgress — the drag&drop
// primitive: reorder + cross-module move on an active course; lesson
// status and IDs survive.
func TestService_ApplyStructure_ReorderPreservesProgress(t *testing.T) {
	svc, repo, c := seedGranular(t)
	ctx := context.Background()

	err := svc.ApplyStructure(ctx, c.ID, []course.ModuleOrder{
		{ModuleID: "m2", LessonIDs: []string{"l3", "l2"}},
		{ModuleID: "m1", LessonIDs: []string{"l1"}},
	})
	require.NoError(t, err)
	assert.Equal(t, 1, repo.modules["m2"].Position)
	assert.Equal(t, 2, repo.modules["m1"].Position)
	assert.Equal(t, "m2", repo.lessons["l2"].ModuleID, "lesson moved across modules")
	assert.Equal(t, 2, repo.lessons["l2"].Position)
	assert.Equal(t, course.LessonOpen, repo.lessons["l2"].Status, "progress preserved")
	assert.Equal(t, course.LessonDone, repo.lessons["l1"].Status, "progress preserved")
}

func TestService_ApplyStructure_Gates(t *testing.T) {
	svc, _, c := seedGranular(t)
	ctx := context.Background()
	full := []course.ModuleOrder{
		{ModuleID: "m1", LessonIDs: []string{"l1", "l2"}},
		{ModuleID: "m2", LessonIDs: []string{"l3"}},
	}

	// Unknown course.
	assert.ErrorIs(t, svc.ApplyStructure(ctx, "ghost", full), coursesvc.ErrNotFound)

	// Malformed payload (repo enforces coverage).
	bad := []course.ModuleOrder{{ModuleID: "m1", LessonIDs: []string{"l1"}}}
	assert.ErrorIs(t, svc.ApplyStructure(ctx, c.ID, bad), coursesvc.ErrInvalidInput)

	// Frozen course.
	c.Status = course.StatusArchived
	assert.ErrorIs(t, svc.ApplyStructure(ctx, c.ID, full), coursesvc.ErrTransition)
}
