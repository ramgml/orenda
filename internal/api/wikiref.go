// Package api — wiki page reference resolution ("W42" / slug / UUID).
//
// Every handler that takes a page slug from the URL and is part of the
// agent surface (REST /api/v1/agent/pages/{slug}, MCP orenda_pages_get)
// resolves the path parameter through resolveWikiRef first: "W<N>" tokens
// go through wiki_pages.number, anything else is treated as a slug.
// The trivial user-side lookups use the same helper.
package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/ramgml/orenda/internal/domain/wiki"
)

// resolveWikiRef resolves a page reference from the URL to the page's
// slug. "W42" resolves through wiki_pages.number via WikiService.Resolve;
// anything else is returned as-is (treated as a slug).
//
// Unknown W-refs surface as *wiki.RefNotFoundError ("page W42 not
// found"). Regular slugs pass through unchanged — the caller is
// responsible for 404 handling when the slug doesn't exist.
func resolveWikiRef(ctx context.Context, deps *Dependencies, ref string) (string, error) {
	if _, ok := wiki.ParseRefNumber(ref); ok {
		p, err := deps.WikiService.Resolve(ctx, ref)
		if err != nil {
			return "", err
		}
		return p.Slug, nil
	}
	// Not a W-ref — return as-is (slug or UUID).
	return ref, nil
}

// writeWikiResolveError translates a resolveWikiRef failure: 404 with the
// explicit "page W42 not found" message for W-refs (the agent needs
// to see WHICH ref didn't resolve), the generic not_found body
// otherwise. Non-not-found errors fall through to writeError.
func writeWikiResolveError(w http.ResponseWriter, err error) {
	var refErr *wiki.RefNotFoundError
	if errors.As(err, &refErr) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": refErr.Error()})
		return
	}
	if errors.Is(err, wiki.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not_found"})
		return
	}
	writeError(w, err)
}
