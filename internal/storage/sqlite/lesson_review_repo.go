// Package sqlite — task 18: lesson_reviews (spaced-repetition ladder).
package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/ramgml/orenda/internal/domain/course"
)

// reviewRFC3339 formats/reads the due_at column. RFC3339 UTC keeps the
// stored shape identical to the wire shape; string sorting matches time
// sorting because every writer normalises to UTC.
const reviewTimeLayout = time.RFC3339

func reviewRepoFrom(db *sql.DB) *lessonReviewRepo {
	return &lessonReviewRepo{db: db}
}

type lessonReviewRepo struct {
	db *sql.DB
}

// NewLessonReviewRepository returns the LessonReviewSchedulerRepository
// backed by db. Declared over the concrete type so the cmd/orenda wiring
// can pass it wherever the interface is consumed.
func NewLessonReviewRepository(db *sql.DB) course.LessonReviewSchedulerRepository {
	return reviewRepoFrom(db)
}

func scanReview(row interface{ Scan(...any) error }) (*course.LessonReview, error) {
	var r course.LessonReview
	var dueAt, lastResult, createdAt string
	var completedAt sql.NullString
	if err := row.Scan(&r.ID, &r.LessonID, &r.UserID, &r.Step, &dueAt, &lastResult, &completedAt, &createdAt); err != nil {
		if err == sql.ErrNoRows {
			return nil, course.ErrNotFound
		}
		return nil, err
	}
	due, err := time.Parse(time.RFC3339, dueAt)
	if err != nil {
		return nil, fmt.Errorf("lesson_reviews.due_at %q: %w", dueAt, err)
	}
	r.DueAt = due.UTC()
	if lastResult != "" {
		r.LastResult = course.LessonReviewResult(lastResult)
	}
	if completedAt.Valid && completedAt.String != "" {
		ct, err := time.Parse(time.RFC3339, completedAt.String)
		if err != nil {
			return nil, fmt.Errorf("lesson_reviews.completed_at %q: %w", completedAt.String, err)
		}
		r.CompletedAt = &ct
	}
	r.CreatedAt = parseTimeLite(createdAt)
	return &r, nil
}

func (r *lessonReviewRepo) CreateReview(ctx context.Context, rev *course.LessonReview) error {
	if rev.ID == "" {
		rev.ID = newUUID()
	}
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO lesson_reviews (id, lesson_id, user_id, step, due_at, last_result, completed_at)
		 VALUES (?, ?, ?, ?, ?, NULL, NULL)`,
		rev.ID, rev.LessonID, rev.UserID, rev.Step, rev.DueAt.UTC().Format(reviewTimeLayout),
	)
	if err != nil {
		return fmt.Errorf("lesson_reviews.CreateReview: %w", err)
	}
	return nil
}

func (r *lessonReviewRepo) GetReview(ctx context.Context, id string) (*course.LessonReview, error) {
	row := r.db.QueryRowContext(ctx,
		`SELECT id, lesson_id, user_id, step, due_at,
		        COALESCE(last_result, ''),
		        COALESCE(completed_at, ''),
		        created_at
		 FROM lesson_reviews WHERE id = ?`, id)
	rev, err := scanReview(row)
	if err != nil {
		return nil, fmt.Errorf("lesson_reviews.GetReview: %w", err)
	}
	return rev, nil
}

func (r *lessonReviewRepo) UpdateReview(ctx context.Context, rev *course.LessonReview) error {
	var completedAt any
	if rev.CompletedAt != nil {
		completedAt = rev.CompletedAt.UTC().Format(reviewTimeLayout)
	}
	var lastResult any
	if rev.LastResult != "" {
		lastResult = string(rev.LastResult)
	}
	res, err := r.db.ExecContext(ctx,
		`UPDATE lesson_reviews
		 SET step = ?, due_at = ?, last_result = ?, completed_at = ?
		 WHERE id = ?`,
		rev.Step, rev.DueAt.UTC().Format(reviewTimeLayout), lastResult,
		completedAt, rev.ID,
	)
	if err != nil {
		return fmt.Errorf("lesson_reviews.UpdateReview: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return course.ErrNotFound
	}
	return nil
}

func (r *lessonReviewRepo) ListDueReviews(ctx context.Context, userID string, until time.Time) ([]*course.PendingLessonReview, error) {
	const q = `SELECT rv.id, rv.lesson_id, l.title, c.id, c.title, rv.step, rv.due_at,
	                  COALESCE(rv.last_result, '')
	           FROM lesson_reviews rv
	           JOIN course_lessons l ON l.id = rv.lesson_id
	           JOIN course_modules m ON m.id = l.module_id
	           JOIN courses c        ON c.id = m.course_id
	           WHERE rv.user_id = ? AND rv.completed_at IS NULL AND rv.due_at <= ?
	           ORDER BY rv.due_at`
	rows, err := r.db.QueryContext(ctx, q, userID, until.UTC().Format(reviewTimeLayout))
	if err != nil {
		return nil, fmt.Errorf("lesson_reviews.ListDueReviews: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []*course.PendingLessonReview
	for rows.Next() {
		var p course.PendingLessonReview
		var dueAt, lastResult string
		if err := rows.Scan(&p.ID, &p.LessonID, &p.LessonTitle, &p.CourseID, &p.CourseTitle,
			&p.Step, &dueAt, &lastResult); err != nil {
			return nil, fmt.Errorf("lesson_reviews.ListDueReviews scan: %w", err)
		}
		due, err := time.Parse(time.RFC3339, dueAt)
		if err != nil {
			return nil, fmt.Errorf("lesson_reviews.ListDueReviews due_at %q: %w", dueAt, err)
		}
		p.DueAt = due.UTC()
		p.LastResult = course.LessonReviewResult(lastResult)
		out = append(out, &p)
	}
	return out, rows.Err()
}

func (r *lessonReviewRepo) CountDueReviewsInCourse(ctx context.Context, courseID string, until time.Time) (int, error) {
	const q = `SELECT COUNT(*) FROM lesson_reviews rv
	           JOIN course_lessons l ON l.id = rv.lesson_id
	           JOIN course_modules m ON m.id = l.module_id
	           WHERE m.course_id = ? AND rv.completed_at IS NULL AND rv.due_at <= ?`
	var n int
	if err := r.db.QueryRowContext(ctx, q, courseID, until.UTC().Format(reviewTimeLayout)).Scan(&n); err != nil {
		return 0, fmt.Errorf("lesson_reviews.CountDueReviewsInCourse: %w", err)
	}
	return n, nil
}
