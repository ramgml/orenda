package project

import (
	"context"
	"fmt"
	"strconv"
)

// RefNotFoundError is returned when a P-prefixed project reference ("P7")
// matches no project. Is(ErrNotFound) reports true so the existing 404
// plumbing keeps working; the message names the ref so an agent
// reading the error sees "project P7 not found" instead of a bare
// "not found".
type RefNotFoundError struct {
	Ref string
}

// Error implements error.
func (e *RefNotFoundError) Error() string {
	return fmt.Sprintf("project %s not found", e.Ref)
}

// Is implements the errors.Is contract: a RefNotFoundError matches
// ErrNotFound so handlers can keep matching on the single sentinel.
func (e *RefNotFoundError) Is(target error) bool {
	return target == ErrNotFound
}

// ParseProjectRef parses a P-prefixed human project reference: "P7" or
// "p7" → (7, true). The prefix is case-insensitive (P/p). The digit
// sequence must be ≥1 digit and positive.
//
// UUIDs are never confused because they contain '-' separators and hex
// letters.
func ParseProjectRef(ref string) (int, bool) {
	if len(ref) < 2 {
		return 0, false
	}
	prefix := ref[0]
	if prefix != 'P' && prefix != 'p' {
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

// ResolveProjectRef returns the project identified by ref. ref may be a
// project UUID or a P-prefixed number ("P7" / "p7").
//
// Unknown P-refs surface as *RefNotFoundError ("project P7 not
// found"); unknown ids as ErrNotFound. Both match ErrNotFound via
// errors.Is.
//
// This is the single resolver every project-id-taking surface should
// funnel through (user REST, agent REST, MCP id arguments) so the
// "P<N>" convention behaves identically everywhere.
func ResolveProjectRef(ctx context.Context, repo Repository, ref string) (*Project, error) {
	if n, ok := ParseProjectRef(ref); ok {
		p, err := repo.GetByNumber(ctx, n)
		if err != nil {
			if err == ErrNotFound {
				return nil, &RefNotFoundError{Ref: ref}
			}
			return nil, err
		}
		return p, nil
	}
	return repo.GetProject(ctx, ref)
}
