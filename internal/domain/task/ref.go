package task

import (
	"context"
	"fmt"
	"strconv"
	"strings"
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

// LegacyRefNotFoundError is returned when a task reference in the
// pre-Task-48 syntax ("#42" or bare "42") matches no task. Like
// RefNotFoundError it matches ErrNotFound so the 404 plumbing keeps
// working; the message names the T-ref replacement so agents
// following stale docs get a direct fix instead of diagnosing a
// missing route.
type LegacyRefNotFoundError struct {
	Ref string
}

// Error implements error.
func (e *LegacyRefNotFoundError) Error() string {
	digits, _ := legacyRefDigits(e.Ref)
	return fmt.Sprintf("task %s not found; use %q — task refs are T-prefixed since Task 48", e.Ref, "T"+digits)
}

// Is implements the errors.Is contract against ErrNotFound.
func (e *LegacyRefNotFoundError) Is(target error) bool {
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

// legacyRefDigits reports the digits of a pre-Task-48 task ref and
// whether ref has that shape: "#42" → ("42", true), "42" →
// ("42", true). T-refs, UUIDs and anything else → ("", false).
func legacyRefDigits(ref string) (string, bool) {
	s := strings.TrimPrefix(ref, "#")
	if s == "" {
		return "", false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return "", false
		}
	}
	return s, true
}

// ResolveRef returns the task identified by ref. ref may be a task
// UUID or a T-prefixed number ("T42" / "t42"). Legacy "#42" and bare
// "42" are rejected (Task 48 cutover).
//
// Unknown T-refs surface as *RefNotFoundError ("task T42 not
// found"); legacy-shaped refs ("#42", bare "42") that match nothing
// surface as *LegacyRefNotFoundError, whose message names the T-ref
// replacement; any other unknown id stays a bare ErrNotFound. All
// match ErrNotFound via errors.Is.
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
	tr, err := repo.GetByID(ctx, ref)
	if err == nil {
		return tr, nil
	}
	if err == ErrNotFound {
		if _, ok := legacyRefDigits(ref); ok {
			return nil, &LegacyRefNotFoundError{Ref: ref}
		}
	}
	return nil, err
}
