package task

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

// RefNotFoundError is returned when a numeric task reference ("#42" /
// "42") matches no task. Is(ErrNotFound) reports true so the existing
// 404 plumbing keeps working; the message names the number so an agent
// reading the error sees "task #42 not found" instead of a bare
// "not found".
type RefNotFoundError struct {
	Number int
}

// Error implements error.
func (e *RefNotFoundError) Error() string {
	return fmt.Sprintf("task #%d not found", e.Number)
}

// Is implements the errors.Is contract: a RefNotFoundError matches
// ErrNotFound so handlers can keep matching on the single sentinel.
func (e *RefNotFoundError) Is(target error) bool {
	return target == ErrNotFound
}

// ParseRefNumber parses a human task reference: "#42" or "42" →
// (42, true). Anything else — UUIDs, empty strings, mixed tokens,
// non-positive numbers — returns (0, false).
//
// UUIDv7 ids always contain '-' separators and hex letters, so a
// pure-digit token can never collide with a real task id; the two
// namespaces are disjoint by construction.
func ParseRefNumber(ref string) (int, bool) {
	s := strings.TrimPrefix(ref, "#")
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
// UUID, a bare human number ("42"), or the display form ("#42").
// Unknown numeric refs surface as *RefNotFoundError ("task #N not
// found"); unknown ids as ErrNotFound. Both match ErrNotFound via
// errors.Is.
//
// This is the single resolver every task-id-taking surface should
// funnel through (agent REST, agent CLI via REST, MCP id arguments,
// and the trivial user-REST lookups) so the "#N" convention behaves
// identically everywhere.
func ResolveRef(ctx context.Context, repo Repository, ref string) (*Task, error) {
	if n, ok := ParseRefNumber(ref); ok {
		tr, err := repo.GetByNumber(ctx, n)
		if err != nil {
			if err == ErrNotFound {
				return nil, &RefNotFoundError{Number: n}
			}
			return nil, err
		}
		return tr, nil
	}
	return repo.GetByID(ctx, ref)
}
