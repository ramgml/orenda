package review_test

// Task 18: spaced-repetition service tests.
//
// The ladder is deterministic, so the tests pin exact dates:
//
//   - ScheduleReview seeds step 0 due at completedAt + 1d.
//   - RecordResult pass advances (step 1 → +3d, 2 → +7d …); a pass on
//     the last step (35d) closes the ladder (completed_at set).
//   - RecordResult fail resets to step 0 due now + 1d.
//   - Recording on a closed ladder → ErrConflict; unknown id →
//     ErrNotFound; missing result → ErrInvalidInput.
//   - CompleteLesson seeds the chain via the ReviewScheduler seam and
//     does NOT fail when the scheduler errors (audit gap, not a user
//     error).

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ramgml/orenda/internal/domain/course"
	coursesvc "github.com/ramgml/orenda/internal/service/course"
	"github.com/ramgml/orenda/internal/service/review"
)

// minimalCourseRepo is the narrow course.Repository surface CompleteLesson
// touches. (The course package's own stub lives in a different test
// package; we keep a local copy to stay decoupled.)
type minimalCourseRepo struct {
	course.Repository // panics on every method the tests don't touch
	lessons           map[string]*course.Lesson
	owners            map[string]string // moduleID -> owner
}

func (m *minimalCourseRepo) GetLesson(_ context.Context, id string) (*course.Lesson, error) {
	l, ok := m.lessons[id]
	if !ok {
		return nil, course.ErrNotFound
	}
	cp := *l
	return &cp, nil
}

func (m *minimalCourseRepo) UpdateLesson(_ context.Context, l *course.Lesson) error {
	cp := *l
	m.lessons[l.ID] = &cp
	return nil
}

func (m *minimalCourseRepo) ListLessons(_ context.Context, moduleID string) ([]*course.Lesson, error) {
	var out []*course.Lesson
	for _, l := range m.lessons {
		if l.ModuleID == moduleID {
			cp := *l
			out = append(out, &cp)
		}
	}
	return out, nil
}

func (m *minimalCourseRepo) ModuleCourseOwner(_ context.Context, moduleID string) (string, error) {
	o, ok := m.owners[moduleID]
	if !ok {
		return "", course.ErrNotFound
	}
	return o, nil
}

type memReviewRepo struct {
	reviews map[string]*course.LessonReview
	nextID  int
}

func newMemReviewRepo() *memReviewRepo {
	return &memReviewRepo{reviews: map[string]*course.LessonReview{}}
}

func (m *memReviewRepo) CreateReview(_ context.Context, r *course.LessonReview) error {
	m.nextID++
	if r.ID == "" {
		r.ID = "rv-" + string(rune('0'+m.nextID))
	}
	cp := *r
	m.reviews[r.ID] = &cp
	return nil
}

func (m *memReviewRepo) GetReview(_ context.Context, id string) (*course.LessonReview, error) {
	r, ok := m.reviews[id]
	if !ok {
		return nil, course.ErrNotFound
	}
	cp := *r
	return &cp, nil
}

func (m *memReviewRepo) UpdateReview(_ context.Context, r *course.LessonReview) error {
	if _, ok := m.reviews[r.ID]; !ok {
		return course.ErrNotFound
	}
	cp := *r
	m.reviews[r.ID] = &cp
	return nil
}

func (m *memReviewRepo) ListDueReviews(_ context.Context, userID string, until time.Time) ([]*course.PendingLessonReview, error) {
	var out []*course.PendingLessonReview
	for _, r := range m.reviews {
		if r.UserID == userID && r.CompletedAt == nil && !r.DueAt.After(until) {
			out = append(out, &course.PendingLessonReview{
				ID: r.ID, LessonID: r.LessonID, Step: r.Step,
				DueAt: r.DueAt, LastResult: r.LastResult,
			})
		}
	}
	return out, nil
}

func (m *memReviewRepo) CountDueReviewsInCourse(_ context.Context, courseID string, until time.Time) (int, error) {
	n := 0
	for _, r := range m.reviews {
		if r.CompletedAt == nil && !r.DueAt.After(until) {
			n++
		}
	}
	return n, nil
}

// recordingScheduler adapts the review service into the course service's
// ReviewScheduler seam and records every call.
type recordingScheduler struct {
	svc     *review.Service
	calls   []reviewCall
	failErr error // when set, every call returns it
}

type reviewCall struct {
	lessonID, userID string
	completedAt      time.Time
}

func (r *recordingScheduler) ScheduleReview(ctx context.Context, lessonID, userID string, completedAt time.Time) error {
	r.calls = append(r.calls, reviewCall{lessonID, userID, completedAt})
	if r.failErr != nil {
		return r.failErr
	}
	return r.svc.ScheduleReview(ctx, lessonID, userID, completedAt)
}

// ---- ladder table test -------------------------------------------------------

func TestReviewService_LadderTable(t *testing.T) {
	completedAt := time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)

	// Ladder walk with deterministic dates. done marks the step that
	// closes the ladder (pass on the last one).
	type attempt struct {
		result     course.LessonReviewResult
		wantStep   int
		wantDue    time.Time
		wantClosed bool
	}

	t.Run("all_pass_walks_to_completion", func(t *testing.T) {
		repo := newMemReviewRepo()
		svc := review.New(repo)
		require.NoError(t, svc.ScheduleReview(context.Background(), "l1", "u1", completedAt))

		at := func(day int) time.Time {
			return time.Date(2026, 9, day, 10, 0, 0, 0, time.UTC)
		}
		attempts := []attempt{
			// Answered Sep 2 10:00 (due instant) → next due +3d = Sep 5 10:00.
			{result: course.ReviewPass, wantStep: 1, wantDue: at(5)},
			// Sep 5 → +7d = Sep 12.
			{result: course.ReviewPass, wantStep: 2, wantDue: at(12)},
			// Sep 12 → +16d = Sep 28.
			{result: course.ReviewPass, wantStep: 3, wantDue: at(28)},
			// Sep 28 → +35d = Nov 2.
			{result: course.ReviewPass, wantStep: 4, wantDue: time.Date(2026, 11, 2, 10, 0, 0, 0, time.UTC)},
			// Nov 2: pass on the last step closes the ladder.
			{result: course.ReviewPass, wantStep: 4, wantClosed: true},
		}

		// Seed row: due Sep 2 10:00 (completedAt + 1d keeps the wall
		// clock of the completion instant), step 0.
		seed, err := repo.GetReview(context.Background(), "rv-1")
		require.NoError(t, err)
		assert.Equal(t, 0, seed.Step)
		assert.Equal(t, completedAt.Add(24*time.Hour), seed.DueAt, "step 0 due = completedAt + 1d")
		assert.Nil(t, seed.CompletedAt)

		revID := "rv-1"
		// The student answers exactly at the due instant.
		now := completedAt.Add(24 * time.Hour)
		for i, a := range attempts {
			updated, err := svc.RecordResult(context.Background(), revID, "u1", a.result, now)
			require.NoError(t, err, "attempt %d", i)
			assert.Equal(t, a.wantStep, updated.Step, "attempt %d", i)
			if a.wantClosed {
				require.NotNil(t, updated.CompletedAt, "attempt %d: ladder must close", i)
				assert.Equal(t, now, *updated.CompletedAt)
				assert.Equal(t, a.wantStep, updated.Step, "step stays on the last rung")
			} else {
				assert.Nil(t, updated.CompletedAt, "attempt %d must not close the ladder", i)
				assert.Equal(t, a.wantDue, updated.DueAt, "attempt %d", i)
			}
			// Each attempt happens exactly when the review is due.
			now = a.wantDue
		}

		// Closed ladder refuses further results.
		_, err = svc.RecordResult(context.Background(), revID, "u1", course.ReviewPass, now)
		require.ErrorIs(t, err, review.ErrConflict)
	})

	t.Run("fail_resets_to_step0", func(t *testing.T) {
		repo := newMemReviewRepo()
		svc := review.New(repo)
		require.NoError(t, svc.ScheduleReview(context.Background(), "l1", "u1", completedAt))

		at := func(day int) time.Time {
			return time.Date(2026, 9, day, 10, 0, 0, 0, time.UTC)
		}
		now := at(2)
		// Pass → step 1 (due +3d = Sep 5 10:00).
		up, err := svc.RecordResult(context.Background(), "rv-1", "u1", course.ReviewPass, now)
		require.NoError(t, err)
		assert.Equal(t, 1, up.Step)
		assert.Equal(t, at(5), up.DueAt)

		// Fail at Sep 5 → step 0, due Sep 6 10:00 (+1d).
		now = at(5)
		up, err = svc.RecordResult(context.Background(), "rv-1", "u1", course.ReviewFail, now)
		require.NoError(t, err)
		assert.Equal(t, 0, up.Step)
		assert.Equal(t, at(6), up.DueAt)
		assert.Equal(t, course.ReviewFail, up.LastResult)
		assert.Nil(t, up.CompletedAt)

		// Pass again climbs from step 0 to step 1, not step 2.
		now = at(6)
		up, err = svc.RecordResult(context.Background(), "rv-1", "u1", course.ReviewPass, now)
		require.NoError(t, err)
		assert.Equal(t, 1, up.Step)
		assert.Equal(t, at(9), up.DueAt)
	})
}

func TestReviewService_RecordResultValidation(t *testing.T) {
	repo := newMemReviewRepo()
	svc := review.New(repo)
	require.NoError(t, svc.ScheduleReview(context.Background(), "l1", "u1", time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)))

	now := time.Date(2026, 9, 2, 10, 0, 0, 0, time.UTC)

	// Empty/invalid result → ErrInvalidInput.
	for _, bad := range []course.LessonReviewResult{"", "maybe", "PASS"} {
		_, err := svc.RecordResult(context.Background(), "rv-1", "u1", bad, now)
		require.ErrorIs(t, err, review.ErrInvalidInput, "result %q", bad)
	}

	// Unknown id → ErrNotFound (handler maps to 404; foreign ids take
	// the same path — existence is not leaked).
	_, err := svc.RecordResult(context.Background(), "nope", "u1", course.ReviewPass, now)
	require.ErrorIs(t, func() error {
		_, ferr := svc.RecordResult(context.Background(), "rv-1", "someone-else", course.ReviewPass, now)
		return ferr
	}(), review.ErrNotFound, "foreign owner must be 404-indistinguishable")
	require.ErrorIs(t, err, review.ErrNotFound)
}

func TestReviewService_ListDueBoundary(t *testing.T) {
	repo := newMemReviewRepo()
	svc := review.New(repo)
	completedAt := time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)
	require.NoError(t, svc.ScheduleReview(context.Background(), "l1", "u1", completedAt))

	dueAt := completedAt.Add(24 * time.Hour)

	// Exactly at due_at → included (<= boundary).
	got, err := svc.ListDue(context.Background(), "u1", dueAt)
	require.NoError(t, err)
	require.Len(t, got, 1)

	// One second before → excluded.
	got, err = svc.ListDue(context.Background(), "u1", dueAt.Add(-time.Second))
	require.NoError(t, err)
	assert.Empty(t, got)

	// Other users see nothing.
	got, err = svc.ListDue(context.Background(), "u2", dueAt)
	require.NoError(t, err)
	assert.Empty(t, got)
}

// TestCompleteLesson_SchedulesReview — task 18: CompleteLesson seeds the
// ladder through the ReviewScheduler seam with the ModuleCourseOwner
// user and the lesson's fresh CompletedAt. A scheduler error must NOT
// fail the completion (audit gap, never a user-visible failure).
func TestCompleteLesson_SchedulesReview(t *testing.T) {
	t.Run("seeds_step0_via_seam", func(t *testing.T) {
		repo := &minimalCourseRepo{
			lessons: map[string]*course.Lesson{},
			owners:  map[string]string{"m1": "owner-1"},
		}
		repo.lessons["l1"] = &course.Lesson{ID: "l1", ModuleID: "m1", Title: "Intro", Status: course.LessonOpen}
		repo.owners["m1"] = "owner-1"
		// Rewrite the service clock indirectly: CompleteLesson stamps
		// time.Now() itself; the scheduler receives that stamp.
		sched := &recordingScheduler{svc: review.New(newMemReviewRepo())}
		svc := coursesvc.New(repo).WithReviews(sched)

		_, err := svc.CompleteLesson(context.Background(), "l1")
		require.NoError(t, err)
		require.Len(t, sched.calls, 1, "exactly one review seeded")
		assert.Equal(t, "l1", sched.calls[0].lessonID)
		assert.Equal(t, "owner-1", sched.calls[0].userID, "owner resolves through ModuleCourseOwner")
		require.NotNil(t, repo.lessons["l1"].CompletedAt)
		assert.WithinDuration(t, time.Now().UTC(), sched.calls[0].completedAt, time.Minute)
	})

	t.Run("scheduler_error_does_not_fail_completion", func(t *testing.T) {
		repo := &minimalCourseRepo{
			lessons: map[string]*course.Lesson{},
			owners:  map[string]string{"m1": "owner-1"},
		}
		repo.lessons["l1"] = &course.Lesson{ID: "l1", ModuleID: "m1", Title: "Intro", Status: course.LessonOpen}
		repo.owners["m1"] = "owner-1"
		sched := &recordingScheduler{svc: review.New(newMemReviewRepo()), failErr: errors.New("db down")}
		svc := coursesvc.New(repo).WithReviews(sched)

		updated, err := svc.CompleteLesson(context.Background(), "l1")
		require.NoError(t, err, "review seeding must never fail the completion")
		assert.Equal(t, course.LessonDone, updated.Status)
	})

	t.Run("nil_seam_keeps_old_semantics", func(t *testing.T) {
		repo := &minimalCourseRepo{
			lessons: map[string]*course.Lesson{},
			owners:  map[string]string{"m1": "owner-1"},
		}
		repo.lessons["l1"] = &course.Lesson{ID: "l1", ModuleID: "m1", Title: "Intro", Status: course.LessonOpen}
		svc := coursesvc.New(repo)

		updated, err := svc.CompleteLesson(context.Background(), "l1")
		require.NoError(t, err)
		assert.Equal(t, course.LessonDone, updated.Status)
	})
}

func TestReviewService_ReviewStepsDays(t *testing.T) {
	// Pin the deterministic ladder — a change here is a breaking
	// schedule change and must be conscious.
	assert.Equal(t, []int{1, 3, 7, 16, 35}, review.ReviewStepsDays)

	due, err := review.NextDueAt(0, time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	require.NoError(t, err)
	assert.Equal(t, time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC), due)

	_, err = review.NextDueAt(99, time.Now())
	require.Error(t, err)
}
