package storage

// Driver-neutral storage sentinels (wiki:storage-adapters D5).
//
// Consumers above the storage layer match these via errors.Is instead
// of driver-package errors. The values are defined once — in the
// shared repository layer (internal/storage/sqlite) — and re-exported
// here, so sentinel identity holds no matter which name a caller uses.
// Both dialects classify into them: SQLite by modernc message text,
// postgres by SQLSTATE (23505 → ErrUniqueViolation, 23503 →
// ErrFKViolation).

import (
	"github.com/ramgml/orenda/internal/storage/sqlite"
)

// Sentinels every driver translates its native errors into.
var (
	// ErrUniqueViolation reports a UNIQUE constraint failure.
	ErrUniqueViolation = sqlite.ErrUniqueViolation
	// ErrFKViolation reports a foreign-key constraint failure.
	ErrFKViolation = sqlite.ErrFKViolation
	// ErrTokenNotFound reports a missing api_tokens row.
	ErrTokenNotFound = sqlite.ErrTokenNotFound
	// ErrLockTaken reports that another agent holds the task lock.
	ErrLockTaken = sqlite.ErrLockTaken
	// ErrLockNotFound reports that the lock target (task/agent) is gone.
	ErrLockNotFound = sqlite.ErrLockNotFound
	// ErrLockNotHeld reports releasing/submitting a lock one doesn't hold.
	ErrLockNotHeld = sqlite.ErrLockNotHeld
)

// IsUniqueViolation reports whether err is a driver-level UNIQUE
// constraint failure on either dialect: PostgreSQL unique_violation
// (SQLSTATE 23505, matched via errors.As on *pgconn.PgError) or the
// modernc driver's SQLite message text. The classification itself lives
// in the shared repository layer (sqlite.IsUniqueViolation) so every
// repository boundary translates both representations.
func IsUniqueViolation(err error) bool { return sqlite.IsUniqueViolation(err) }

// IsFKViolation reports whether err is a driver-level foreign-key
// constraint failure (postgres: SQLSTATE 23503).
func IsFKViolation(err error) bool { return sqlite.IsFKViolation(err) }
