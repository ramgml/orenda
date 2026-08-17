package sqlite

import (
	"context"
	"database/sql"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ramgml/orenda/internal/domain/course"
	"github.com/ramgml/orenda/internal/domain/user"
)

// Phase 18: course repository smoke tests.
//
// We focus on the small surface that catches the most likely
// regressions: lifecycle CRUD, the SubmitCurriculum atomic swap,
// and the Progress count.

func setupCourseOwner(t *testing.T, db *sql.DB) string {
	t.Helper()
	users := NewUserRepository(db)
	u := &user.User{
		Email:        "course-owner@x.com",
		PasswordHash: "x",
		DisplayName:  "CourseOwner",
	}
	require.NoError(t, users.Create(context.Background(), u))
	return u.ID
}

func TestCourseRepo_BasicLifecycle(t *testing.T) {
	db := setupUserDB(t)
	owner := setupCourseOwner(t, db)
	repo := NewCourseRepository(db)
	ctx := context.Background()

	// Create draft.
	c := &course.Course{
		Title:    "Learn Rust",
		IntentMD: "One month, 3x/week",
		Level:    "beginner",
		Pace:     "regular",
		Status:   course.StatusDraft,
		OwnerID:  owner,
	}
	require.NoError(t, repo.CreateCourse(ctx, c))
	assert.NotEmpty(t, c.ID)

	got, err := repo.GetCourse(ctx, c.ID)
	require.NoError(t, err)
	assert.Equal(t, "Learn Rust", got.Title)
	assert.Equal(t, course.StatusDraft, got.Status)

	// Update → review.
	got.Status = course.StatusReview
	require.NoError(t, repo.UpdateCourse(ctx, got))
	got2, _ := repo.GetCourse(ctx, c.ID)
	assert.Equal(t, course.StatusReview, got2.Status)

	// List by owner.
	all, err := repo.ListCourses(ctx, owner)
	require.NoError(t, err)
	require.Len(t, all, 1)
	assert.Equal(t, c.ID, all[0].ID)

	// Delete.
	require.NoError(t, repo.DeleteCourse(ctx, c.ID))
	_, err = repo.GetCourse(ctx, c.ID)
	assert.ErrorIs(t, err, course.ErrNotFound)
}

func TestCourseRepo_SubmitCurriculum(t *testing.T) {
	db := setupUserDB(t)
	owner := setupCourseOwner(t, db)
	repo := NewCourseRepository(db)
	ctx := context.Background()

	// Create course in draft, then submit a curriculum.
	c := &course.Course{Title: "X", IntentMD: "y", Status: course.StatusDraft, OwnerID: owner}
	require.NoError(t, repo.CreateCourse(ctx, c))

	modules := []*course.Module{
		{ID: "m1", CourseID: c.ID, Title: "Basics", Position: 1},
		{ID: "m2", CourseID: c.ID, Title: "Advanced", Position: 2},
	}
	lessons := []*course.Lesson{
		{ID: "l1", ModuleID: "m1", Title: "Hello", Position: 1, Status: course.LessonLocked},
		{ID: "l2", ModuleID: "m1", Title: "Types", Position: 2, Status: course.LessonLocked},
		{ID: "l3", ModuleID: "m2", Title: "Traits", Position: 1, Status: course.LessonLocked},
	}
	require.NoError(t, repo.SubmitCurriculum(ctx, c.ID, modules, lessons, nil))

	// Modules + lessons persisted.
	modList, err := repo.ListModules(ctx, c.ID)
	require.NoError(t, err)
	assert.Len(t, modList, 2)

	allLessons, err := repo.ListLessonsInCourse(ctx, c.ID)
	require.NoError(t, err)
	assert.Len(t, allLessons, 3)

	// Progress: 0 done, 3 total.
	prog, err := repo.Progress(ctx, c.ID)
	require.NoError(t, err)
	assert.Equal(t, 3, prog.LessonsTotal)
	assert.Equal(t, 0, prog.LessonsDone)
}

func TestCourseRepo_Progress(t *testing.T) {
	db := setupUserDB(t)
	owner := setupCourseOwner(t, db)
	repo := NewCourseRepository(db)
	ctx := context.Background()

	c := &course.Course{Title: "X", Status: course.StatusDraft, OwnerID: owner}
	require.NoError(t, repo.CreateCourse(ctx, c))
	modules := []*course.Module{{ID: "m1", CourseID: c.ID, Title: "M", Position: 1}}
	lessons := []*course.Lesson{
		{ID: "l1", ModuleID: "m1", Title: "A", Position: 1, Status: course.LessonOpen},
		{ID: "l2", ModuleID: "m1", Title: "B", Position: 2, Status: course.LessonLocked},
	}
	require.NoError(t, repo.SubmitCurriculum(ctx, c.ID, modules, lessons, nil))

	l1, err := repo.GetLesson(ctx, "l1")
	require.NoError(t, err)
	l1.Status = course.LessonDone
	require.NoError(t, repo.UpdateLesson(ctx, l1))

	prog, err := repo.Progress(ctx, c.ID)
	require.NoError(t, err)
	assert.Equal(t, 2, prog.LessonsTotal)
	assert.Equal(t, 1, prog.LessonsDone)
}

// TestCourseRepo_SubmitCurriculum_WithQuizzes — Phase 27.6: the swap
// carries quizzes; they land in course_quizzes under the right lesson
// and survive a second swap (idempotency by ID reuse).
func TestCourseRepo_SubmitCurriculum_WithQuizzes(t *testing.T) {
	db := setupUserDB(t)
	owner := setupCourseOwner(t, db)
	repo := NewCourseRepository(db)
	ctx := context.Background()

	c := &course.Course{Title: "Quiz course", Status: course.StatusDraft, OwnerID: owner}
	require.NoError(t, repo.CreateCourse(ctx, c))

	modules := []*course.Module{{ID: "m1", CourseID: c.ID, Title: "M", Position: 1}}
	lessons := []*course.Lesson{
		{ID: "l1", ModuleID: "m1", Title: "L", Position: 1, Status: course.LessonLocked},
	}
	quizzes := []*course.Quiz{
		{ID: "q1", LessonID: "l1", Position: 1, QuestionMD: "Q1", ExpectedMD: "A", Kind: course.QuizExact},
		{ID: "q2", LessonID: "l1", Position: 2, QuestionMD: "Q2", ExpectedMD: "B", Kind: course.QuizExact},
	}
	require.NoError(t, repo.SubmitCurriculum(ctx, c.ID, modules, lessons, quizzes))

	list, err := repo.ListQuizzesInCourse(ctx, c.ID)
	require.NoError(t, err)
	require.Len(t, list, 2)
	assert.Equal(t, "q1", list[0].ID)
	assert.Equal(t, "q2", list[1].ID)
	assert.Equal(t, "Q1", list[0].QuestionMD)

	// Second swap with the same IDs — they should be reused, not
	// duplicated. This is what makes "edit a draft" round-trip-safe.
	require.NoError(t, repo.SubmitCurriculum(ctx, c.ID, modules, lessons, quizzes))
	list, err = repo.ListQuizzesInCourse(ctx, c.ID)
	require.NoError(t, err)
	assert.Len(t, list, 2, "quizzes must not be duplicated on idempotent swap")
}

// TestCourseRepo_SubmitCurriculum_DropsStaleQuizzes — when a swap
// removes a quiz, the old row is gone (cascade through modules).
func TestCourseRepo_SubmitCurriculum_DropsStaleQuizzes(t *testing.T) {
	db := setupUserDB(t)
	owner := setupCourseOwner(t, db)
	repo := NewCourseRepository(db)
	ctx := context.Background()

	c := &course.Course{Title: "T", Status: course.StatusDraft, OwnerID: owner}
	require.NoError(t, repo.CreateCourse(ctx, c))

	modules := []*course.Module{{ID: "m1", CourseID: c.ID, Title: "M", Position: 1}}
	lessons := []*course.Lesson{{ID: "l1", ModuleID: "m1", Title: "L", Position: 1, Status: course.LessonLocked}}

	first := []*course.Quiz{
		{ID: "q1", LessonID: "l1", Position: 1, QuestionMD: "Keep", Kind: course.QuizExact},
		{ID: "q2", LessonID: "l1", Position: 2, QuestionMD: "Drop", Kind: course.QuizExact},
	}
	require.NoError(t, repo.SubmitCurriculum(ctx, c.ID, modules, lessons, first))

	// Second swap: keep only q1.
	second := []*course.Quiz{
		{ID: "q1", LessonID: "l1", Position: 1, QuestionMD: "Keep", Kind: course.QuizExact},
	}
	require.NoError(t, repo.SubmitCurriculum(ctx, c.ID, modules, lessons, second))

	list, err := repo.ListQuizzesInCourse(ctx, c.ID)
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.Equal(t, "q1", list[0].ID)
}

// TestCourseRepo_CreateQuiz_AutoPosition — position 0 means "append at
// the end"; the repo picks the next slot atomically and writes it back
// into the quiz struct so the caller knows where it landed.
func TestCourseRepo_CreateQuiz_AutoPosition(t *testing.T) {
	db := setupUserDB(t)
	owner := setupCourseOwner(t, db)
	repo := NewCourseRepository(db)
	ctx := context.Background()

	c := &course.Course{Title: "T", Status: course.StatusDraft, OwnerID: owner}
	require.NoError(t, repo.CreateCourse(ctx, c))
	require.NoError(t, repo.SubmitCurriculum(ctx, c.ID,
		[]*course.Module{{ID: "m1", CourseID: c.ID, Title: "M", Position: 1}},
		[]*course.Lesson{{ID: "l1", ModuleID: "m1", Title: "L", Position: 1, Status: course.LessonLocked}},
		nil,
	))

	q1 := &course.Quiz{LessonID: "l1", QuestionMD: "A", Kind: course.QuizExact}
	require.NoError(t, repo.CreateQuiz(ctx, q1))
	assert.Equal(t, 1, q1.Position, "first quiz gets position 1")

	q2 := &course.Quiz{LessonID: "l1", QuestionMD: "B", Kind: course.QuizExact}
	require.NoError(t, repo.CreateQuiz(ctx, q2))
	assert.Equal(t, 2, q2.Position, "second quiz gets position 2")

	q3 := &course.Quiz{LessonID: "l1", QuestionMD: "C", Kind: course.QuizExact, Position: 7}
	require.NoError(t, repo.CreateQuiz(ctx, q3))
	assert.Equal(t, 7, q3.Position, "explicit position is preserved")
}

// ---- Phase 30.13: granular CRUD + structure reorder -----------------

// seedCourseTree builds a 2-module / 3-lesson / 1-quiz course and
// returns the course ID. Shared by the granular-CRUD tests below.
func seedCourseTree(t *testing.T, repo course.Repository, owner string) string {
	t.Helper()
	ctx := context.Background()
	c := &course.Course{Title: "T", Status: course.StatusDraft, OwnerID: owner}
	require.NoError(t, repo.CreateCourse(ctx, c))
	require.NoError(t, repo.SubmitCurriculum(ctx, c.ID,
		[]*course.Module{
			{ID: "m1", CourseID: c.ID, Title: "Basics", Position: 1},
			{ID: "m2", CourseID: c.ID, Title: "Advanced", Position: 2},
		},
		[]*course.Lesson{
			{ID: "l1", ModuleID: "m1", Title: "Hello", Position: 1, Status: course.LessonDone},
			{ID: "l2", ModuleID: "m1", Title: "Types", Position: 2, Status: course.LessonOpen},
			{ID: "l3", ModuleID: "m2", Title: "Traits", Position: 1, Status: course.LessonLocked},
		},
		[]*course.Quiz{
			{ID: "q1", LessonID: "l1", Position: 1, QuestionMD: "Q1", ExpectedMD: "A", Kind: course.QuizExact},
		},
	))
	return c.ID
}

func TestCourseRepo_ModuleGranularCRUD(t *testing.T) {
	db := setupUserDB(t)
	owner := setupCourseOwner(t, db)
	repo := NewCourseRepository(db)
	ctx := context.Background()
	courseID := seedCourseTree(t, repo, owner)

	// GetModule round-trip.
	m, err := repo.GetModule(ctx, "m1")
	require.NoError(t, err)
	assert.Equal(t, courseID, m.CourseID)
	assert.Equal(t, "Basics", m.Title)

	// UpdateModule writes title/description, keeps position.
	m.Title = "Fundamentals"
	m.Description = "start here"
	m.Position = 99 // must be ignored — ordering belongs to ApplyStructure
	require.NoError(t, repo.UpdateModule(ctx, m))
	got, err := repo.GetModule(ctx, "m1")
	require.NoError(t, err)
	assert.Equal(t, "Fundamentals", got.Title)
	assert.Equal(t, "start here", got.Description)
	assert.Equal(t, 1, got.Position, "UpdateModule must not touch position")

	// Unknown id → ErrNotFound.
	_, err = repo.GetModule(ctx, "nope")
	assert.ErrorIs(t, err, course.ErrNotFound)
	assert.ErrorIs(t, repo.UpdateModule(ctx, &course.Module{ID: "nope", Title: "x"}), course.ErrNotFound)
	assert.ErrorIs(t, repo.DeleteModule(ctx, "nope"), course.ErrNotFound)

	// DeleteModule cascades lessons + quizzes.
	require.NoError(t, repo.DeleteModule(ctx, "m1"))
	lessons, err := repo.ListLessonsInCourse(ctx, courseID)
	require.NoError(t, err)
	assert.Len(t, lessons, 1, "l1 and l2 must cascade with m1")
	quizzes, err := repo.ListQuizzesInCourse(ctx, courseID)
	require.NoError(t, err)
	assert.Empty(t, quizzes, "q1 must cascade with l1")
}

func TestCourseRepo_LessonAndQuizGranularCRUD(t *testing.T) {
	db := setupUserDB(t)
	owner := setupCourseOwner(t, db)
	repo := NewCourseRepository(db)
	ctx := context.Background()
	courseID := seedCourseTree(t, repo, owner)

	// UpdateQuiz writes the three content fields, keeps position.
	q, err := repo.GetQuiz(ctx, "q1")
	require.NoError(t, err)
	q.QuestionMD = "Q1'"
	q.ExpectedMD = "B"
	q.Kind = course.QuizOpen
	require.NoError(t, repo.UpdateQuiz(ctx, q))
	got, err := repo.GetQuiz(ctx, "q1")
	require.NoError(t, err)
	assert.Equal(t, "Q1'", got.QuestionMD)
	assert.Equal(t, "B", got.ExpectedMD)
	assert.Equal(t, course.QuizOpen, got.Kind)
	assert.Equal(t, 1, got.Position)

	// DeleteQuiz + DeleteLesson, unknown ids → ErrNotFound.
	require.NoError(t, repo.DeleteQuiz(ctx, "q1"))
	_, err = repo.GetQuiz(ctx, "q1")
	assert.ErrorIs(t, err, course.ErrNotFound)
	assert.ErrorIs(t, repo.DeleteQuiz(ctx, "q1"), course.ErrNotFound)

	require.NoError(t, repo.DeleteLesson(ctx, "l3"))
	_, err = repo.GetLesson(ctx, "l3")
	assert.ErrorIs(t, err, course.ErrNotFound)
	assert.ErrorIs(t, repo.DeleteLesson(ctx, "l3"), course.ErrNotFound)

	lessons, err := repo.ListLessonsInCourse(ctx, courseID)
	require.NoError(t, err)
	assert.Len(t, lessons, 2)
}

// TestCourseRepo_ApplyStructure — the Phase 30.13 reorder primitive:
// full-coverage payload reorders modules and moves lessons across
// modules; partial/foreign/duplicate payloads are rejected without
// writing anything.
func TestCourseRepo_ApplyStructure(t *testing.T) {
	db := setupUserDB(t)
	owner := setupCourseOwner(t, db)
	repo := NewCourseRepository(db)
	ctx := context.Background()
	courseID := seedCourseTree(t, repo, owner)

	// Happy path: swap module order, move l2 into m2, flip l1/l3.
	require.NoError(t, repo.ApplyStructure(ctx, courseID, []course.ModuleOrder{
		{ModuleID: "m2", LessonIDs: []string{"l3", "l2"}},
		{ModuleID: "m1", LessonIDs: []string{"l1"}},
	}))

	modules, err := repo.ListModules(ctx, courseID)
	require.NoError(t, err)
	require.Len(t, modules, 2)
	assert.Equal(t, "m2", modules[0].ID)
	assert.Equal(t, 1, modules[0].Position)
	assert.Equal(t, "m1", modules[1].ID)
	assert.Equal(t, 2, modules[1].Position)

	l2, err := repo.GetLesson(ctx, "l2")
	require.NoError(t, err)
	assert.Equal(t, "m2", l2.ModuleID, "lesson moved across modules")
	assert.Equal(t, 2, l2.Position)
	assert.Equal(t, course.LessonOpen, l2.Status, "reorder must preserve lesson status (student progress)")

	l3, err := repo.GetLesson(ctx, "l3")
	require.NoError(t, err)
	assert.Equal(t, 1, l3.Position)

	// Rejections: nothing is written on a bad payload.
	bad := []struct {
		name    string
		payload []course.ModuleOrder
	}{
		{"missing module", []course.ModuleOrder{
			{ModuleID: "m1", LessonIDs: []string{"l1", "l2"}},
		}},
		{"unknown module", []course.ModuleOrder{
			{ModuleID: "m1", LessonIDs: []string{"l1", "l2"}},
			{ModuleID: "ghost", LessonIDs: []string{"l3"}},
		}},
		{"duplicate module", []course.ModuleOrder{
			{ModuleID: "m1", LessonIDs: []string{"l1", "l2"}},
			{ModuleID: "m1", LessonIDs: []string{"l3"}},
		}},
		{"missing lesson", []course.ModuleOrder{
			{ModuleID: "m1", LessonIDs: []string{"l1"}},
			{ModuleID: "m2", LessonIDs: []string{"l3"}},
		}},
		{"unknown lesson", []course.ModuleOrder{
			{ModuleID: "m1", LessonIDs: []string{"l1", "ghost"}},
			{ModuleID: "m2", LessonIDs: []string{"l3"}},
		}},
		{"duplicate lesson across modules", []course.ModuleOrder{
			{ModuleID: "m1", LessonIDs: []string{"l1", "l2"}},
			{ModuleID: "m2", LessonIDs: []string{"l3", "l1"}},
		}},
	}
	for _, tc := range bad {
		t.Run(tc.name, func(t *testing.T) {
			err := repo.ApplyStructure(ctx, courseID, tc.payload)
			assert.ErrorIs(t, err, course.ErrInvalidInput)
			// State untouched: m2 still first from the happy path.
			modules, merr := repo.ListModules(ctx, courseID)
			require.NoError(t, merr)
			assert.Equal(t, "m2", modules[0].ID)
		})
	}
}
