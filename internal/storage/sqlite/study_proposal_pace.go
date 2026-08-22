// Package sqlite — Phase 32.12: pace-adaptation helper.
//
// StudyProposalRepo gains CountAcceptedInWindow so enrichActiveCourse
// can compute the "target velocity" (accepted proposals per week) for
// the drift classifier. Drift compares actual_velocity_per_week vs
// target_velocity_per_week; without the target the classifier falls
// back to on_track per the wiki (don't panic without data).
package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// CountAcceptedInWindow returns the count of study_proposals for a
// course whose status='accepted' AND whose created_at is inside
// [since, ∞). The window is open-ended (no upper bound) because the
// caller (enrichActiveCourse) only cares about "how much did the
// student commit to" — closed-window counting of "when did the user
// accept" is what drift measures, and accepted proposals imply
// commitment regardless of when they were accepted.
//
// courseID="" means "all courses the owner has access to" — but the
// drift signal is per-course, so callers always pass a non-empty id.
// The empty-string case is preserved for symmetry with the rest of
// the repo API and future "overall planner" use.
//
// SQLite counts as INTEGER; the result fits in int even for years
// of activity.
func (r *studyProposalRepo) CountAcceptedInWindow(ctx context.Context, courseID string, since time.Time) (int, error) {
	if courseID == "" {
		return 0, fmt.Errorf("study_proposal.CountAcceptedInWindow: courseID required")
	}
	var n int
	err := r.db.QueryRowContext(ctx,
		`SELECT COUNT(*)
		 FROM study_proposals
		 WHERE course_id = ?
		   AND status = ?
		   AND created_at >= ?`,
		courseID, "accepted", since.UTC().Format(time.RFC3339),
	).Scan(&n)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, nil
		}
		return 0, fmt.Errorf("study_proposal.CountAcceptedInWindow: %w", err)
	}
	return n, nil
}
