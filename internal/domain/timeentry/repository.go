package timeentry

import (
	"context"
	"time"
)

// Repository persists TimeEntry rows.
type Repository interface {
	// Create inserts e. Returns the row with CreatedAt populated.
	Create(ctx context.Context, e *TimeEntry) (*TimeEntry, error)

	// GetByID returns the entry with the given id.
	GetByID(ctx context.Context, id string) (*TimeEntry, error)

	// Update persists changes to an existing row.
	Update(ctx context.Context, e *TimeEntry) error

	// Delete removes the row. Returns ErrNotFound if no such row.
	Delete(ctx context.Context, id string) error

	// FindOpen returns the single open entry (EndedAt IS NULL) for the
	// given agent, or nil if none. Used to enforce the one-timer-per-agent
	// invariant.
	FindOpen(ctx context.Context, agentID string) (*TimeEntry, error)

	// ListByTask returns all entries (open + closed) for the given task
	// ordered by started_at DESC.
	ListByTask(ctx context.Context, taskID string) ([]*TimeEntry, error)

	// ListByAgent returns entries for the given agent in the [from, to)
	// window, ordered by started_at DESC. Used by the time report.
	ListByAgent(ctx context.Context, agentID string, from, to time.Time) ([]*TimeEntry, error)

	// ListByDay is a convenience for the per-day report view: same as
	// ListByAgent with from=dayStart, to=dayEnd.
	ListByDay(ctx context.Context, agentID string, day time.Time) ([]*TimeEntry, error)
}
