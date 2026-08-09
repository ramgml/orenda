// Package sqlite — shared helpers used by every repository implementation.
package sqlite

import (
	"strings"
	"time"

	"github.com/google/uuid"
)

// sqliteTimeLayout is the format produced by SQLite's datetime('now')
// function. We keep parsing it for backwards-compat with rows written
// before the modernc driver took over (Phase 11+). New writes use
// the same space-separated layout so time.Time values bound by the
// modernc.org/sqlite driver compare correctly in WHERE clauses
// (both produce "YYYY-MM-DD HH:MM:SS[.fffff][±HH:MM]").
//
// All timestamps in the DB are UTC.
const sqliteTimeLayout = "2006-01-02 15:04:05"

// parseTime converts a SQLite timestamp string to time.Time.
//
// Returns the zero time for empty input — callers must use sql.NullString
// or a pointer when NULL is a valid value.
func parseTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	// Try the space-separated layout (what we write) first, then any
	// legacy variant. Both are interpreted as UTC.
	if t, err := time.ParseInLocation(sqliteTimeLayout, s, time.UTC); err == nil {
		return t
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.UTC()
	}
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return t.UTC()
	}
	return time.Time{}
}

// formatTime converts time.Time to a SQLite string format that's
// lexicographically comparable with what the modernc.org/sqlite driver
// produces when binding a time.Time argument. Both produce the form
// "YYYY-MM-DD HH:MM:SS[.fffff][±HH:MM]", so WHERE start_at < ? works
// with the same data the Go layer wrote.
//
// Returns empty string for zero time so the column stays NULL via
// the SQL COALESCE pattern in the repository code.
func formatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(sqliteTimeLayout)
}

// newUUID returns a UUIDv7 string suitable for primary keys.
//
// UUIDv7 is time-ordered which keeps indexes efficient on INSERT-heavy
// workloads. The library falls back to v4 if v7 is not supported on the
// platform.
func newUUID() string {
	id, err := uuid.NewV7()
	if err != nil {
		// v7 should always succeed; fall back to v4 just in case.
		return uuid.NewString()
	}
	return id.String()
}

// isUniqueViolation reports whether err is a SQLite UNIQUE constraint failure.
//
// The modernc driver returns errors prefixed with "constraint failed: UNIQUE
// constraint failed:". We detect this with a string match — there is no
// dedicated error code in modernc (no equivalent of pq.Error.Code).
func isUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "UNIQUE constraint failed")
}

// isFKViolation reports whether err is a SQLite foreign-key violation.
func isFKViolation(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "FOREIGN KEY constraint failed")
}
