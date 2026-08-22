// Package api — project reference resolution ("P7" / UUID).
//
// Every handler that takes a project id from the URL or body resolves
// the parameter through resolveProjectRef first: "P<N>" tokens go
// through projects.number, anything else is treated as the project UUID.
package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/ramgml/orenda/internal/domain/project"
)

// resolveProjectRef resolves a project reference from the URL/body to the
// full project. "P7" resolves through projects.number; anything else is a
// UUID lookup. Returns the loaded *Project so callers that also need the
// full row (get, patch, agent-get) avoid a second GetProject round-trip.
// Callers that only need the ID access .ID on the returned project.
//
// Unknown P-refs surface as *project.RefNotFoundError ("project P7 not
// found"); unknown ids as project.ErrNotFound.
func resolveProjectRef(ctx context.Context, deps *Dependencies, ref string) (*project.Project, error) {
	return project.ResolveProjectRef(ctx, deps.Projects, ref)
}

// writeProjectResolveError translates a resolveProjectRef failure: 404
// with the explicit "project P7 not found" message for P-refs (the agent
// needs to see WHICH ref didn't resolve), the generic not_found body
// otherwise. Non-not-found errors fall through to writeError.
func writeProjectResolveError(w http.ResponseWriter, err error) {
	var refErr *project.RefNotFoundError
	if errors.As(err, &refErr) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": refErr.Error()})
		return
	}
	if errors.Is(err, project.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not_found"})
		return
	}
	writeError(w, err)
}
