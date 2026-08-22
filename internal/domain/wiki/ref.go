package wiki

import (
	"context"
	"fmt"
	"strconv"
)

// RefNotFoundError is returned when a W-prefixed wiki page reference ("W42")
// matches no page. Is(ErrNotFound) reports true so the existing 404
// plumbing keeps working; the message names the ref so an agent
// reading the error sees "page W42 not found" instead of a bare
// "not found".
type RefNotFoundError struct {
	Ref string
}

// Error implements error.
func (e *RefNotFoundError) Error() string {
	return fmt.Sprintf("page %s not found", e.Ref)
}

// Is implements the errors.Is contract: a RefNotFoundError matches
// ErrNotFound so handlers can keep matching on the single sentinel.
func (e *RefNotFoundError) Is(target error) bool {
	return target == ErrNotFound
}

// ParseRefNumber parses a W-prefixed human wiki page reference: "W42" or
// "w42" → (42, true). The prefix is case-insensitive (W/w). The digit
// sequence must be ≥1 digit and positive.
//
// UUIDs are never confused because they contain '-' separators and hex letters.
func ParseRefNumber(ref string) (int, bool) {
	if len(ref) < 2 {
		return 0, false
	}
	prefix := ref[0]
	if prefix != 'W' && prefix != 'w' {
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

// ResolveRef returns the page identified by ref. ref may be a page
// UUID, a slug, or a W-prefixed number ("W42" / "w42").
//
// W-refs resolve through wiki_pages.number; slugs and UUIDs go to
// the existing GetBySlug/GetByID lookups. Unknown W-refs surface as
// *RefNotFoundError ("page W42 not found"); unknown slugs/IDs as
// ErrNotFound. Both match ErrNotFound via errors.Is.
//
// This is the single resolver every page-id-taking surface should
// funnel through (agent REST /agent/pages/{slug}, MCP orenda_pages_get,
// user REST pages) so the "W<N>" convention behaves identically everywhere.
func ResolveRef(ctx context.Context, repo Repository, ref string) (*Page, error) {
	if n, ok := ParseRefNumber(ref); ok {
		p, err := repo.GetByNumber(ctx, n)
		if err != nil {
			if err == ErrNotFound {
				return nil, &RefNotFoundError{Ref: ref}
			}
			return nil, err
		}
		return p, nil
	}
	// Try slug first; if not found, try as ID.
	p, err := repo.GetBySlug(ctx, ref)
	if err == nil {
		return p, nil
	}
	if err != ErrNotFound {
		return nil, err
	}
	return repo.GetByID(ctx, ref)
}

// IsWRefFormat reports whether ref matches the W<digits> format
// (case-insensitive). Used by slug validation to reject W<N> slugs.
func IsWRefFormat(ref string) bool {
	_, ok := ParseRefNumber(ref)
	return ok
}
