package event

import (
	"context"
	"time"
)

// Repository persists and retrieves Events. Recurrence expansion
// (Occurrences) is the service layer's job; the repo only stores
// master events.
type Repository interface {
	// Create inserts e. Returns the row with CreatedAt/UpdatedAt populated.
	Create(ctx context.Context, e *Event) (*Event, error)

	// GetByID returns the event with the given id.
	GetByID(ctx context.Context, id string) (*Event, error)

	// Update replaces the row with the same id. Returns ErrNotFound if
	// no such row exists.
	Update(ctx context.Context, e *Event) error

	// Delete removes the event by id. Returns ErrNotFound if no row
	// matched.
	Delete(ctx context.Context, id string) error

	// ListInRange returns events whose [StartAt, EndAt] overlaps with
	// [from, to). Optional projectID filters by project; empty means
	// "all projects".
	ListInRange(ctx context.Context, from, to time.Time, projectID string) ([]*Event, error)
}
