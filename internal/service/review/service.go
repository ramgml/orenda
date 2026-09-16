// Package review — task 18: the spaced-repetition ladder service.
//
// The plan is server-side and deterministic: a completed lesson spawns a
// review chain at 1 / 3 / 7 / 16 / 35 days (ReviewStepsDays). Each attempt
// either advances the ladder (pass) or resets it (fail); a pass on the
// last step closes the chain (completed_at set). The service never
// touches lesson progress — the lesson stays done, reviews are a separate
// dimension.
package review

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/ramgml/orenda/internal/domain/course"
)

// ReviewStepsDays is the deterministic ladder. step N is due
// ReviewStepsDays[N] days after the previous pass; step 0 is due 1 day
// after lesson completion. Kept as a package constant (no config file)
// until a real operator need shows up — YAGNI.
var ReviewStepsDays = []int{1, 3, 7, 16, 35}

// Sentinel errors the API layer translates to HTTP status codes.
var (
	// ErrNotFound: unknown review id (or one owned by someone else —
	// the handler treats both as 404 to avoid existence leaks).
	ErrNotFound = errors.New("review service: not found")
	// ErrInvalidInput: missing/invalid result value.
	ErrInvalidInput = errors.New("review service: invalid input")
	// ErrConflict: the ladder is already closed (completed_at set).
	ErrConflict = errors.New("review service: review already completed")
)

// Service applies the ladder rules on top of the review repository.
// The HTTP layer delegates here so the rules apply regardless of caller.
type Service struct {
	Repo course.LessonReviewSchedulerRepository
}

func New(repo course.LessonReviewSchedulerRepository) *Service {
	return &Service{Repo: repo}
}

// NextDueAt returns the due date for the given ladder step, computed
// from now. step must index ReviewStepsDays.
func NextDueAt(step int, now time.Time) (time.Time, error) {
	if step < 0 || step >= len(ReviewStepsDays) {
		return time.Time{}, fmt.Errorf("review: step %d outside ladder (len %d)", step, len(ReviewStepsDays))
	}
	return now.Add(time.Duration(ReviewStepsDays[step]) * 24 * time.Hour), nil
}

// ScheduleReview is the CompleteLesson trigger: seed the ladder with a
// single step-0 row due tomorrow. The scheduler seam is nil-safe on the
// course-service side; when wired this never fails the completion itself
// (the course service swallows the error and logs — a missing review
// chain must not roll back a finished lesson).
func (s *Service) ScheduleReview(ctx context.Context, lessonID, userID string, completedAt time.Time) error {
	due, err := NextDueAt(0, completedAt)
	if err != nil {
		return err
	}
	rev := &course.LessonReview{
		LessonID: lessonID,
		UserID:   userID,
		Step:     0,
		DueAt:    due,
	}
	if err := s.Repo.CreateReview(ctx, rev); err != nil {
		return fmt.Errorf("review.ScheduleReview: %w", err)
	}
	return nil
}

// ListDue returns the user's open reviews with due_at <= until
// (the "reviews due" queue behind GET /reviews/due and /today).
func (s *Service) ListDue(ctx context.Context, userID string, until time.Time) ([]*course.PendingLessonReview, error) {
	return s.Repo.ListDueReviews(ctx, userID, until)
}

// CountDueInCourse is the agent-side enrichment: how many open reviews
// of the course are due as of until.
func (s *Service) CountDueInCourse(ctx context.Context, courseID string, until time.Time) (int, error) {
	return s.Repo.CountDueReviewsInCourse(ctx, courseID, until)
}

// RecordResult applies the ladder to one attempt:
//
//   - pass: step+1 with due = now + ReviewStepsDays[step+1]; a pass on
//     the last step closes the ladder (completed_at = now).
//   - fail: reset to step 0, due = now + ReviewStepsDays[0].
//
// Ownership is enforced by the caller passing the review id; a foreign
// id surfaces as ErrNotFound (the handler maps it to 404). A closed
// ladder returns ErrConflict (409).
func (s *Service) RecordResult(ctx context.Context, reviewID string, result course.LessonReviewResult, now time.Time) (*course.LessonReview, error) {
	if result != course.ReviewPass && result != course.ReviewFail {
		return nil, ErrInvalidInput
	}
	rev, err := s.Repo.GetReview(ctx, reviewID)
	if err != nil {
		return nil, ErrNotFound
	}
	if rev.CompletedAt != nil {
		return nil, ErrConflict
	}
	if result == course.ReviewPass && rev.Step+1 >= len(ReviewStepsDays) {
		// Pass on the last step: the ladder is walked out.
		rev.Step = len(ReviewStepsDays) - 1
		rev.CompletedAt = &now
	} else if result == course.ReviewPass {
		rev.Step++
		due, err := NextDueAt(rev.Step, now)
		if err != nil {
			return nil, err
		}
		rev.DueAt = due
	} else {
		rev.Step = 0
		due, err := NextDueAt(0, now)
		if err != nil {
			return nil, err
		}
		rev.DueAt = due
	}
	rev.LastResult = result
	if err := s.Repo.UpdateReview(ctx, rev); err != nil {
		return nil, fmt.Errorf("review.RecordResult: %w", err)
	}
	return rev, nil
}
