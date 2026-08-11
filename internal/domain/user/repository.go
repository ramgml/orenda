package user

import "context"

// Repository persists and retrieves Users.
//
// Implementations must be safe for concurrent use; the SQLite store uses a
// single connection per process and serialises writes through the database
// driver's own lock.
type Repository interface {
	// Create inserts u. Returns ErrEmailTaken if the email already exists.
	Create(ctx context.Context, u *User) error

	// GetByID returns the user with the given id or ErrNotFound.
	GetByID(ctx context.Context, id string) (*User, error)

	// GetByEmail returns the user with the given email or ErrNotFound.
	GetByEmail(ctx context.Context, email string) (*User, error)

	// List returns all users ordered by created_at ASC. The slice is empty
	// (not nil) when there are no rows; callers can rely on that.
	List(ctx context.Context) ([]*User, error)

	// Update saves changes to an existing user. Returns ErrNotFound if the
	// id doesn't exist.
	Update(ctx context.Context, u *User) error

	// Delete removes the user by id. Returns ErrNotFound if no row matched.
	Delete(ctx context.Context, id string) error

	// FirstNonSystem returns the first user whose role isn't "system",
	// or ErrNotFound. Used by Phase 16 to address notifications for
	// tasks without a project (Inbox) — there's no project owner in
	// that case, so the lone human owner is the natural recipient.
	// The "first" semantics are deliberate: a single-user install has
	// exactly one such user, and the query is non-deterministic
	// enough that the multi-user case doesn't accidentally privilege
	// one account over another.
	FirstNonSystem(ctx context.Context) (*User, error)
}
