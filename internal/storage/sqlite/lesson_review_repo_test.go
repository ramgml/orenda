package sqlite

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ramgml/orenda/internal/domain/course"
	"github.com/ramgml/orenda/internal/domain/user"
)

// Task 18: lesson_reviews migration + repo.
//
// Pinned contracts:
//  1. Up creates the table with step/due_at defaults, three indexes;
//     down drops them (round-trip).
//  2. CHECK constraint rejects non pass/fail last_result.
//  3. FK: deleting the lesson cascades the review rows.
//  4. Repo round-trip: create → get → update → list-due filtered by
//     user and due_at boundary; completed rows drop out of the queue.
//  5. CountDueReviewsInCourse counts only open + due rows of that course.

func TestMigrate_046LessonReviews(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "orenda.db"), OpenConfig{
		WALMode: true, EnableForeign: true, BusyTimeoutMs: 5000,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	applyUpTo(t, ctx, db, "045_tutor_messages")

	// Minimal course tree: owner → course → module → lesson.
	users := NewUserRepository(db)
	u := &user.User{Email: "r46@x.com", PasswordHash: "x", DisplayName: "R46"}
	require.NoError(t, users.Create(ctx, u))
	_, err = db.ExecContext(ctx,
		`INSERT INTO courses (id, title, owner_id, status) VALUES (?, ?, ?, 'active')`,
		"c-046", "Rust", u.ID)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx,
		`INSERT INTO course_modules (id, course_id, title, position) VALUES (?, ?, ?, 1)`,
		"m-046", "c-046", "Basics")
	require.NoError(t, err)
	_, err = db.ExecContext(ctx,
		`INSERT INTO course_lessons (id, module_id, title, status, position) VALUES (?, ?, ?, 'done', 1)`,
		"l-046", "m-046", "Ownership")
	require.NoError(t, err)

	body, err := MigrationsFS.ReadFile("migrations/046_lesson_reviews.sql")
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, string(body))
	require.NoError(t, err)

	// Contract 1: indexes exist.
	indexes := listIndexes(t, ctx, db, "lesson_reviews")
	assert.Contains(t, indexes, "idx_lesson_reviews_due_at")
	assert.Contains(t, indexes, "idx_lesson_reviews_lesson")
	assert.Contains(t, indexes, "idx_lesson_reviews_user_due")

	// Contract 2: CHECK rejects an invalid result; defaults hold.
	_, err = db.ExecContext(ctx,
		`INSERT INTO lesson_reviews (id, lesson_id, user_id, due_at, last_result)
		 VALUES (?, ?, ?, ?, 'maybe')`, "rv-bad", "l-046", u.ID, "2026-01-01T00:00:00Z")
	require.Error(t, err, "last_result CHECK must reject invalid values")

	_, err = db.ExecContext(ctx,
		`INSERT INTO lesson_reviews (id, lesson_id, user_id, due_at) VALUES (?, ?, ?, ?)`,
		"rv-046", "l-046", u.ID, "2026-01-01T00:00:00Z")
	var step int
	var lastResult sql.NullString
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT step, last_result FROM lesson_reviews WHERE id = ?`, "rv-046").Scan(&step, &lastResult))
	assert.Equal(t, 0, step, "step defaults to 0")
	assert.False(t, lastResult.Valid, "last_result defaults to NULL")

	// Contract 3: lesson delete cascades.
	_, err = db.ExecContext(ctx, `DELETE FROM course_lessons WHERE id = ?`, "l-046")
	require.NoError(t, err)
	var n int
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM lesson_reviews`).Scan(&n))
	assert.Equal(t, 0, n, "review rows cascade with the lesson")

	// Contract 1 (cont'd): down round-trip.
	down, err := MigrationsFS.ReadFile("migrations/046_lesson_reviews.down.sql")
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, string(down))
	require.NoError(t, err)
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='lesson_reviews'`).Scan(&n))
	assert.Equal(t, 0, n, "down drops lesson_reviews")
}

func setupReviewFixture(t *testing.T) (context.Context, *sql.DB, course.LessonReviewSchedulerRepository, string, string) {
	t.Helper()
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "orenda.db"), OpenConfig{
		WALMode: true, EnableForeign: true, BusyTimeoutMs: 5000,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	require.NoError(t, Migrate(ctx, db, MigrationsFS, "migrations"))
	users := NewUserRepository(db)
	u := &user.User{Email: "reviews@x.com", PasswordHash: "x", DisplayName: "Student"}
	require.NoError(t, users.Create(ctx, u))
	_, err = db.ExecContext(ctx,
		`INSERT INTO courses (id, title, owner_id, status) VALUES (?, ?, ?, 'active')`,
		"c-rev", "Go", u.ID)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx,
		`INSERT INTO course_modules (id, course_id, title, position) VALUES (?, ?, ?, 1)`,
		"m-rev", "c-rev", "Core")
	require.NoError(t, err)
	_, err = db.ExecContext(ctx,
		`INSERT INTO course_lessons (id, module_id, title, status, position) VALUES (?, ?, ?, 'done', 1)`,
		"l-rev", "m-rev", "Goroutines")
	require.NoError(t, err)
	return ctx, db, NewLessonReviewRepository(db), u.ID, "l-rev"
}

func TestReviewRepo_DueQueueRoundTrip(t *testing.T) {
	ctx, _, repo, userID, lessonID := setupReviewFixture(t)

	due := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	rev := &course.LessonReview{LessonID: lessonID, UserID: userID, Step: 0, DueAt: due}
	require.NoError(t, repo.CreateReview(ctx, rev))
	require.NotEmpty(t, rev.ID, "repo mints a UUIDv7 id")

	got, err := repo.GetReview(ctx, rev.ID)
	require.NoError(t, err)
	assert.Equal(t, due, got.DueAt)
	assert.Equal(t, course.LessonReviewResult(""), got.LastResult)
	assert.Nil(t, got.CompletedAt)

	// Pass moves to step 1, due +3d.
	nextDue := due.Add(3 * 24 * time.Hour)
	got.Step = 1
	got.DueAt = nextDue
	got.LastResult = course.ReviewPass
	require.NoError(t, repo.UpdateReview(ctx, got))

	reread, err := repo.GetReview(ctx, rev.ID)
	require.NoError(t, err)
	assert.Equal(t, 1, reread.Step)
	assert.Equal(t, nextDue, reread.DueAt)
	assert.Equal(t, course.ReviewPass, reread.LastResult)

	// Close the ladder.
	closed := nextDue
	reread.Step = 5
	reread.CompletedAt = &closed
	require.NoError(t, repo.UpdateReview(ctx, reread))

	// A completed review drops out of the due queue regardless of due_at.
	future, err := repo.ListDueReviews(ctx, userID, closed.Add(365*24*time.Hour))
	require.NoError(t, err)
	assert.Empty(t, future, "completed_at IS NOT NULL rows are not due")

	// Re-open by clearing completion and due it in the past.
	reread.CompletedAt = nil
	require.NoError(t, repo.UpdateReview(ctx, reread))
	list, err := repo.ListDueReviews(ctx, userID, closed.Add(time.Second))
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.Equal(t, "Goroutines", list[0].LessonTitle)
	assert.Equal(t, "Go", list[0].CourseTitle)
	assert.Equal(t, "c-rev", list[0].CourseID)
	assert.Equal(t, course.ReviewPass, list[0].LastResult)

	// due_at > until is excluded (strict <= boundary).
	before, err := repo.ListDueReviews(ctx, userID, reread.DueAt.Add(-time.Second))
	require.NoError(t, err)
	assert.Empty(t, before)

	// Another user sees nothing.
	other, err := repo.ListDueReviews(ctx, "nobody", closed.Add(24*time.Hour))
	require.NoError(t, err)
	assert.Empty(t, other)

	// UpdateReview on a missing row → ErrNotFound.
	ghost := &course.LessonReview{ID: "nope", LastResult: course.ReviewFail}
	require.ErrorIs(t, repo.UpdateReview(ctx, ghost), course.ErrNotFound)
}

func TestReviewRepo_CountDueReviewsInCourse(t *testing.T) {
	ctx, db, repo, userID, lessonID := setupReviewFixture(t)

	now := time.Now().UTC()
	due := &course.LessonReview{LessonID: lessonID, UserID: userID, Step: 0, DueAt: now.Add(-time.Hour)}
	require.NoError(t, repo.CreateReview(ctx, due))
	notYet := &course.LessonReview{LessonID: lessonID, UserID: userID, Step: 0, DueAt: now.Add(time.Hour)}
	require.NoError(t, repo.CreateReview(ctx, notYet))

	n, err := repo.CountDueReviewsInCourse(ctx, "c-rev", now)
	require.NoError(t, err)
	assert.Equal(t, 1, n, "only the already-due row counts")

	n, err = repo.CountDueReviewsInCourse(ctx, "c-rev", now.Add(2*time.Hour))
	require.NoError(t, err)
	assert.Equal(t, 2, n)

	n, err = repo.CountDueReviewsInCourse(ctx, "other-course", now.Add(2*time.Hour))
	require.NoError(t, err)
	assert.Equal(t, 0, n)

	// Completed rows don't count.
	var completedAt = now
	due.CompletedAt = &completedAt
	require.NoError(t, repo.UpdateReview(ctx, due))
	n, err = repo.CountDueReviewsInCourse(ctx, "c-rev", now.Add(2*time.Hour))
	require.NoError(t, err)
	assert.Equal(t, 1, n)
	_ = db
}
