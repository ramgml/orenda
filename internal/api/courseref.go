// Package api — course reference resolution ("C7" / UUID).
//
// Every handler that takes a course id from the URL or body resolves
// the parameter through resolveCourseRef first: "C<N>" tokens go
// through courses.number, anything else is treated as the course UUID.
// Returns the loaded *Course so callers that also need the full row
// avoid a second GetCourse round-trip.
//
// Unknown C-refs surface as *course.RefNotFoundError ("course C7 not
// found"); unknown ids as course.ErrNotFound.
package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/ramgml/orenda/internal/domain/course"
)

// resolveCourseRef resolves a course reference from the URL/body to the
// full course. "C7" resolves through courses.number; anything else is a
// UUID lookup. Returns the loaded *Course so callers that also need the
// full row avoid a second GetCourse round-trip. Callers that only need
// the ID access .ID on the returned course.
func resolveCourseRef(ctx context.Context, deps *Dependencies, ref string) (*course.Course, error) {
	return course.ResolveCourseRef(ctx, deps.Courses, ref)
}

// writeCourseResolveError translates a resolveCourseRef failure: 404
// with the explicit "course C7 not found" message for C-refs (the agent
// needs to see WHICH ref didn't resolve), the generic not_found body
// otherwise. Non-not-found errors fall through to writeError.
func writeCourseResolveError(w http.ResponseWriter, err error) {
	var refErr *course.RefNotFoundError
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
