// Package api — lesson reference resolution ("L10" / UUID).
//
// Every handler that takes a lesson id from the URL resolves the
// parameter through resolveLessonRef first: "L<N>" tokens go through
// lessons.number, anything else is treated as the lesson UUID.
// Returns the loaded *Lesson so callers that also need the full row
// avoid a second GetLesson round-trip.
//
// Unknown L-refs surface as *course.LessonRefNotFoundError ("lesson L10
// not found"); unknown ids as course.ErrNotFound.
package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/ramgml/orenda/internal/domain/course"
)

// resolveLessonRef resolves a lesson reference from the URL to the
// full lesson. "L10" resolves through lessons.number; anything else is
// a UUID lookup. Returns the loaded *Lesson so callers that also need
// the full row avoid a second GetLesson round-trip.
func resolveLessonRef(ctx context.Context, deps *Dependencies, ref string) (*course.Lesson, error) {
	return course.ResolveLessonRef(ctx, deps.Courses, ref)
}

// writeLessonResolveError translates a resolveLessonRef failure: 404
// with the explicit "lesson L10 not found" message for L-refs (the
// agent needs to see WHICH ref didn't resolve), the generic not_found
// body otherwise. Non-not-found errors fall through to writeError.
func writeLessonResolveError(w http.ResponseWriter, err error) {
	var refErr *course.LessonRefNotFoundError
	if errors.As(err, &refErr) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": refErr.Error()})
		return
	}
	if errors.Is(err, course.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not_found"})
		return
	}
	writeError(w, err)
}
