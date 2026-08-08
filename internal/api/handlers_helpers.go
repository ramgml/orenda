// Package api — small helpers shared across handlers.
package api

import (
	"time"
)

// parseOptionalTime parses RFC3339 timestamps from request bodies.
//
// Returns nil for empty strings so the caller can distinguish "unset" from
// "explicitly zero".
func parseOptionalTime(s string) *time.Time {
	if s == "" {
		return nil
	}
	// Accept both RFC3339 and the SQLite-stored "YYYY-MM-DD HH:MM:SS".
	for _, layout := range []string{time.RFC3339, "2006-01-02 15:04:05"} {
		if t, err := time.Parse(layout, s); err == nil {
			return &t
		}
	}
	return nil
}
