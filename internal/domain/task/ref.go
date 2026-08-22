package task

import (
	"context"
	"fmt"
	"strconv"
)

// RefNotFoundError is returned when a T-prefixed task reference ("T42")
// matches no task. Is(ErrNotFound) reports true so the existing 404
// plumbing keeps working; the message names the ref so an agent
// reading the error sees "task T42 not found" instead of a bare
// "not found".
type RefNotFoundError struct {
	Ref string
}

// Error implements error.
func (e *RefNotFoundError) Error() string {
	return fmt.Sprintf("task %s not found", e.Ref)
}

// Is implements the errors.Is contract: a RefNotFoundError matches
// ErrNotFound so handlers can keep matching on the single sentinel.
func (e *RefNotFoundError) Is(target error) bool {
	return target == ErrNotFound
}

// ParseRefNumber parses a T-prefixed human task reference: "T42" or
// "t42" → (42, true). The prefix is case-insensitive (T/t). The
// digit sequence must be ≥1 digit and positive.
//
// Legacy forms "#42" and bare "42" are intentionally rejected — this
// is the breaking change from Task 48. UUIDs are never confused
// because they contain '-' separators and hex letters.
func ParseRefNumber(ref string) (int, bool) {
	if len(ref) < 2 {
		return 0, false
	}
	prefix := ref[0]
	if prefix != 'T' && prefix != 't' {
		return 0, false
	}
	s := ref[1:]
	if s == "" {
		return 0, false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, false
		}
	}
	n, err := strconv.Atoi(s)
	if err != nil || n <= 0 {
		return 0, false
	}
	return n, true
}

// ResolveRef returns the task identified by ref. ref may be a task
// UUID or a T-prefixed number ("T42" / "t42"). Legacy "#42" and bare
// "42" are rejected (Task 48 cutover).
//
// Unknown T-refs surface as *RefNotFoundError ("task T42 not
// found"); unknown ids as ErrNotFound. Both match ErrNotFound via
// errors.Is.
//
// This is the single resolver every task-id-taking surface should
// funnel through (agent REST, agent CLI via REST, MCP id arguments,
// and the trivial user-REST lookups) so the "T<N>" convention
// behaves identically everywhere.
func ResolveRef(ctx context.Context, repo Repository, ref string) (*Task, error) {
	if n, ok := ParseRefNumber(ref); ok {
		tr, err := repo.GetByNumber(ctx, n)
		if err != nil {
			if err == ErrNotFound {
				return nil, &RefNotFoundError{Ref: ref}
			}
			return nil, err
		}
		return tr, nil
	}
	return repo.GetByID(ctx, ref)
}
