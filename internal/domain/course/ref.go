package course

import (
	"context"
	"fmt"
	"strconv"
)

// RefNotFoundError is returned when a C-prefixed course reference ("C7")
// matches no course. Is(ErrNotFound) reports true so the existing 404
// plumbing keeps working; the message names the ref so an agent
// reading the error sees "course C7 not found" instead of a bare
// "not found".
type RefNotFoundError struct {
	Ref string
}

// Error implements error.
func (e *RefNotFoundError) Error() string {
	return fmt.Sprintf("course %s not found", e.Ref)
}

// Is implements the errors.Is contract: a RefNotFoundError matches
// ErrNotFound so handlers can keep matching on the single sentinel.
func (e *RefNotFoundError) Is(target error) bool {
	return target == ErrNotFound
}

// LessonRefNotFoundError is returned when an L-prefixed lesson reference
// ("L10") matches no lesson. Same contract as RefNotFoundError but
// for lessons.
type LessonRefNotFoundError struct {
	Ref string
}

// Error implements error.
func (e *LessonRefNotFoundError) Error() string {
	return fmt.Sprintf("lesson %s not found", e.Ref)
}

// Is implements the errors.Is contract.
func (e *LessonRefNotFoundError) Is(target error) bool {
	return target == ErrNotFound
}

// ParseCourseRef parses a C-prefixed human course reference: "C7" or
// "c7" → (7, true). The prefix is case-insensitive (C/c). The digit
// sequence must be ≥1 digit and positive.
//
// UUIDs are never confused because they contain '-' separators and hex
// letters.
func ParseCourseRef(ref string) (int, bool) {
	if len(ref) < 2 {
		return 0, false
	}
	prefix := ref[0]
	if prefix != 'C' && prefix != 'c' {
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

// ParseLessonRef parses an L-prefixed human lesson reference: "L10" or
// "l10" → (10, true). The prefix is case-insensitive (L/l). The digit
// sequence must be ≥1 digit and positive.
func ParseLessonRef(ref string) (int, bool) {
	if len(ref) < 2 {
		return 0, false
	}
	prefix := ref[0]
	if prefix != 'L' && prefix != 'l' {
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

// ResolveCourseRef returns the course identified by ref. ref may be a
// course UUID or a C-prefixed number ("C7" / "c7").
//
// Unknown C-refs surface as *RefNotFoundError ("course C7 not
// found"); unknown ids as ErrNotFound. Both match ErrNotFound via
// errors.Is.
//
// This is the single resolver every course-id-taking surface should
// funnel through (user REST, agent REST, MCP id arguments) so the
// "C<N>" convention behaves identically everywhere.
func ResolveCourseRef(ctx context.Context, repo Repository, ref string) (*Course, error) {
	if n, ok := ParseCourseRef(ref); ok {
		c, err := repo.GetCourseByNumber(ctx, n)
		if err != nil {
			if err == ErrNotFound {
				return nil, &RefNotFoundError{Ref: ref}
			}
			return nil, err
		}
		return c, nil
	}
	return repo.GetCourse(ctx, ref)
}

// ResolveLessonRef returns the lesson identified by ref. ref may be a
// lesson UUID or an L-prefixed number ("L10" / "l10").
//
// Unknown L-refs surface as *LessonRefNotFoundError ("lesson L10 not
// found"); unknown ids as ErrNotFound. Both match ErrNotFound via
// errors.Is.
func ResolveLessonRef(ctx context.Context, repo Repository, ref string) (*Lesson, error) {
	if n, ok := ParseLessonRef(ref); ok {
		l, err := repo.GetLessonByNumber(ctx, n)
		if err != nil {
			if err == ErrNotFound {
				return nil, &LessonRefNotFoundError{Ref: ref}
			}
			return nil, err
		}
		return l, nil
	}
	return repo.GetLesson(ctx, ref)
}
