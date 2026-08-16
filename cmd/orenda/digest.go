// Package main — Phase 30.5 weekly digest scheduler.
//
// The scheduler ticks every `interval` (default 7 days) and:
//
//  1. Picks every active owner user (single-owner in this build —
//     multi-user support is deferred to the multi-user era).
//  2. Queries the storage layer for the period stats (tasks done,
//     created, awaiting, overdue, comments, active timers).
//  3. Renders a notifier.DigestStats via notifier.RenderWeeklyDigest.
//  4. Emits a notifier.Event so the existing notification pipeline
//     delivers the message to every bot the operator subscribed.
//
// Errors per owner are non-fatal — a transient DB failure on one
// user shouldn't kill the goroutine for everyone. The loop logs and
// continues.
//
// The aggregation lives in a free function (computeWeeklyDigestStats)
// so unit tests can exercise it without spinning up a full server.
package main

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"go.uber.org/zap"

	notifier "github.com/ramgml/orenda/internal/service/notifier"
)

// digestScheduler fires weekly digest renders per active owner.
type digestScheduler struct {
	interval time.Duration
	logger   *zap.Logger
	db       *sql.DB
	users    ownerLister
	notifier digestNotifier
}

// ownerLister is the slice of `cmd/orenda/main.go`'s user repo that
// the scheduler needs — keeps the unit testable interface narrow.
type ownerLister interface {
	ListAll(ctx context.Context) ([]ownerRecord, error)
}

// ownerRecord is a thin projection of the user row the scheduler
// needs (id + active flag). We don't import the storage package here
// because cmd/orenda already imports storage/sqlite under a concrete
// name — this keeps the interface minimal.
type ownerRecord struct {
	ID     string
	Active bool
}

// digestNotifier is the slice of notifier.Service the scheduler
// needs. We avoid pulling the full Service into this file because
// the only method the scheduler calls is Notify.
type digestNotifier interface {
	Notify(ctx context.Context, e notifyEvent) error
}

// notifyEvent mirrors notifier.Event's shape but only the fields
// the scheduler actually uses. Keeping it tiny keeps the test
// surface narrow.
type notifyEvent struct {
	Type   string
	UserID string
	Title  string
	Body   string
	Link   string
	Meta   map[string]string
}

// Run blocks until ctx is done. It does the weekly tick. Errors
// are logged but don't crash the goroutine.
func (s *digestScheduler) Run(ctx context.Context) {
	t := time.NewTicker(s.interval)
	defer t.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.fireOnce(ctx)
		}
	}
}

// fireOnce runs a single digest pass. Pulled out of Run so tests can
// exercise it without spinning a ticker.
func (s *digestScheduler) fireOnce(ctx context.Context) {
	owners, err := s.users.ListAll(ctx)
	if err != nil {
		s.logger.Warn("digest: list owners failed", zap.Error(err))
		return
	}
	now := time.Now().UTC()
	for _, o := range owners {
		if !o.Active {
			continue
		}
		stats, err := computeWeeklyDigestStats(ctx, s.db, o.ID, now)
		if err != nil {
			s.logger.Warn("digest: compute failed",
				zap.String("user", o.ID), zap.Error(err))
			continue
		}
		stats.OwnerID = o.ID
		stats.PeriodStart = now.Add(-7 * 24 * time.Hour)
		stats.PeriodEnd = now

		rendered := notifier.RenderWeeklyDigest(stats)
		e := notifyEvent{
			Type:   "digest.weekly",
			UserID: o.ID,
			Title:  rendered.Title,
			Body:   rendered.Body,
			Link:   rendered.Link,
			Meta: map[string]string{
				"title": rendered.Title,
				"body":  rendered.Body,
			},
		}
		if err := s.notifier.Notify(ctx, e); err != nil {
			s.logger.Warn("digest: notify failed",
				zap.String("user", o.ID), zap.Error(err))
		}
	}
}

// computeWeeklyDigestStats runs the six aggregate queries the
// digest renders. Each query is a single round-trip — no per-row
// fetches, no joins across the user's projects. The single-owner
// install only ever has one row, but the loop is already shaped for
// the multi-owner era.
func computeWeeklyDigestStats(ctx context.Context, db *sql.DB, userID string, now time.Time) (notifier.DigestStats, error) {
	weekAgo := now.Add(-7 * 24 * time.Hour)
	out := notifier.DigestStats{}

	// TasksDone — user's tasks transitioned to done during the
	// period. We filter on updated_at because we don't store a
	// separate "done_at" timestamp; the trade-off is that reopening
	// a task and re-completing it within the period counts twice.
	// Acceptable for a digest.
	//
	// Note: tasks use assignee_type / assignee_id (the worker),
	// not a direct owner_id. For single-owner installs this is the
	// same user; for the multi-user era this filters "tasks the
	// owner worked on", which is the right digest signal.
	v, err := countRows(ctx, db, `
		SELECT COUNT(*) FROM tasks
		WHERE assignee_type = 'user'
		  AND assignee_id = ?
		  AND status = 'done'
		  AND updated_at >= ?`,
		userID, weekAgo)
	if err != nil {
		return out, fmt.Errorf("tasks done: %w", err)
	}
	out.TasksDone = v

	v, err = countRows(ctx, db, `
		SELECT COUNT(*) FROM tasks
		WHERE assignee_type = 'user'
		  AND assignee_id = ?
		  AND created_at >= ?`,
		userID, weekAgo)
	if err != nil {
		return out, fmt.Errorf("tasks created: %w", err)
	}
	out.TasksCreated = v

	// TasksAwaitingReview and TasksOverdue are LIVE counts, not
	// period-bounded. An empty review queue is the signal; we want
	// the owner to see "0", not "you had 3 last week".
	v, err = countRows(ctx, db, `
		SELECT COUNT(*) FROM tasks
		WHERE assignee_type = 'user'
		  AND assignee_id = ?
		  AND (status = 'review' OR awaiting = 'human')`,
		userID)
	if err != nil {
		return out, fmt.Errorf("tasks awaiting: %w", err)
	}
	out.TasksAwaitingReview = v

	v, err = countRows(ctx, db, `
		SELECT COUNT(*) FROM tasks
		WHERE assignee_type = 'user'
		  AND assignee_id = ?
		  AND status != 'done'
		  AND due_at IS NOT NULL
		  AND due_at < ?`,
		userID, now)
	if err != nil {
		return out, fmt.Errorf("tasks overdue: %w", err)
	}
	out.TasksOverdue = v

	// CommentsReceived — comments other users / agents left on tasks
	// assigned to this user during the period. We exclude self-comments
	// because counting "you commented on your own task" in the
	// digest would be noise.
	v, err = countRows(ctx, db, `
		SELECT COUNT(*) FROM comments c
		JOIN tasks t ON t.id = c.target_id
		WHERE c.target_type = 'task'
		  AND t.assignee_type = 'user'
		  AND t.assignee_id = ?
		  AND c.author_id != ?
		  AND c.created_at >= ?`,
		userID, userID, weekAgo)
	if err != nil {
		return out, fmt.Errorf("comments received: %w", err)
	}
	out.CommentsReceived = v

	// ActiveTimers counts time_entries with ended_at IS NULL across
	// the whole instance — the operator's view of the dashboard is
	// per-instance, not per-user.
	v, err = countRows(ctx, db, `
		SELECT COUNT(*) FROM time_entries
		WHERE ended_at IS NULL`,
	)
	if err != nil {
		return out, fmt.Errorf("active timers: %w", err)
	}
	out.ActiveTimers = v

	return out, nil
}

// countRows is a tiny helper that runs a scalar COUNT query and
// returns the result. Pulled out so the scheduler's SQL stays
// readable.
func countRows(ctx context.Context, db *sql.DB, q string, args ...any) (int, error) {
	var n int
	if err := db.QueryRowContext(ctx, q, args...).Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}
