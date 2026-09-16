package course

import (
	"context"
	"time"
)

// LessonReviewResult is the outcome of one spaced-repetition attempt.
type LessonReviewResult string

// Lesson review outcomes: ReviewPass advances the ladder one step;
// ReviewFail resets it to step 0.
const (
	ReviewPass LessonReviewResult = "pass"
	ReviewFail LessonReviewResult = "fail"
)

// LessonReview is one entry in the per-lesson spaced-repetition ladder
// (task 18). The row is born in CompleteLesson (step 0, due in 1 day)
// and walks the deterministic server-side ladder on each result:
//
//	pass → step+1, due = now + ReviewStepsDays[new step]
//	fail → step 0, due = now + 1d
//	pass on the last step → completed_at set, ladder closed
//
// A review never mutates lesson progress — the lesson stays done.
type LessonReview struct {
	ID          string
	LessonID    string
	UserID      string
	Step        int
	DueAt       time.Time
	LastResult  LessonReviewResult // "" when no attempt has been recorded yet
	CompletedAt *time.Time         // non-nil once the ladder is fully walked
	CreatedAt   time.Time
}

// PendingLessonReview is the due-queue projection the user-side
// endpoints render: the review plus the lesson/course titles so the
// client never needs a follow-up fetch per row.
type PendingLessonReview struct {
	ID          string
	LessonID    string
	LessonTitle string
	CourseID    string
	CourseTitle string
	Step        int
	DueAt       time.Time
	LastResult  LessonReviewResult
}

// LessonReviewSchedulerRepository is the storage surface the
// review service needs. PendingReviewsDueAfter reports reviews for
// userID whose due_at is in (-inf, until] and completed_at IS NULL;
// the today handler passes start-of-day to compute the overdue flag,
// so the query stays a single range scan.
type LessonReviewSchedulerRepository interface {
	// CreateReview inserts a new step-0 review (the CompleteLesson trigger).
	CreateReview(ctx context.Context, r *LessonReview) error
	// GetReview loads one review by id. Returns ErrNotFound when missing.
	GetReview(ctx context.Context, id string) (*LessonReview, error)
	// UpdateReview persists step/due_at/last_result/completed_at after
	// the service applied the ladder rules.
	UpdateReview(ctx context.Context, r *LessonReview) error
	// ListDueReviews returns the user's open (completed_at IS NULL)
	// reviews with due_at <= until, enriched with lesson/course titles.
	ListDueReviews(ctx context.Context, userID string, until time.Time) ([]*PendingLessonReview, error)
	// CountDueReviewsInCourse counts the course's open reviews that are
	// due as of until — the agent-side pending_reviews enrichment.
	CountDueReviewsInCourse(ctx context.Context, courseID string, until time.Time) (int, error)
}
