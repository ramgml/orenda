// Package api — Phase 5 handlers: wiki pages CRUD + backlinks + FTS5 search.
package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/ramgml/orenda/internal/domain/wiki"
	"github.com/ramgml/orenda/internal/service/search"
	wikiservice "github.com/ramgml/orenda/internal/service/wiki"
)

// ----------------------------------------------------------------------------
// Wiki pages
// ----------------------------------------------------------------------------

// pageInput is the JSON body for create/update.
type pageInput struct {
	Slug      string `json:"slug"`
	Title     string `json:"title"`
	ContentMD string `json:"content_md"`
	ParentID  string `json:"parent_id"`
	Position  int    `json:"position"`
}

// listPagesHandler returns all pages.
func listPagesHandler(deps *Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps.WikiService == nil {
			http.Error(w, "wiki service not wired", http.StatusServiceUnavailable)
			return
		}
		tree, err := deps.WikiService.Tree(r.Context())
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"tree": tree})
	}
}

// savePageHandler creates or updates a page.
//
// Two routing shapes share this handler:
//
//	POST /api/v1/pages          — create. Slug comes from the body.
//	PUT  /api/v1/pages/{slug}   — update. Slug comes from the URL
//	                                (and wins if the body also supplies
//	                                one — keeps the URL as the source of
//	                                truth, which the Save button relies
//	                                on because it sends only title +
//	                                content_md).
func savePageHandler(deps *Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps.WikiService == nil {
			http.Error(w, "wiki service not wired", http.StatusServiceUnavailable)
			return
		}
		var in pageInput
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_json"})
			return
		}
		slug := strings.TrimSpace(in.Slug)
		p := &wiki.Page{
			Slug:      slug,
			Title:     in.Title,
			ContentMD: in.ContentMD,
			ParentID:  in.ParentID,
			Position:  in.Position,
		}
		// For PUT /pages/{slug} the URL slug is authoritative: the Save
		// button sends only title + content_md, so without this the
		// handler would build a page with an empty slug, Validate would
		// reject it with wiki.ErrInvalidInput, and writeError would
		// (before the recent fix) surface it as 500.
		if urlSlug := chi.URLParam(r, "slug"); urlSlug != "" {
			// Resolve W-refs in the URL to the actual slug.
			resolved, err := resolveWikiRef(r.Context(), deps, urlSlug)
			if err != nil {
				writeWikiResolveError(w, err)
				return
			}
			p.Slug = resolved
			if existing, err := deps.WikiService.GetBySlug(r.Context(), resolved); err == nil && existing != nil {
				p.ID = existing.ID
			}
		} else if wiki.IsWRefFormat(p.Slug) {
			// POST /pages: reject W<digits> as a slug — W-refs are
			// resolution-only, slugs remain the canonical identifier
			// for [[links]].
			writeJSON(w, http.StatusUnprocessableEntity, map[string]string{
				"error": "slug_conflicts_with_w_ref",
			})
			return
		}
		got, err := deps.WikiService.Save(r.Context(), p)
		if err != nil {
			if errors.Is(err, wikiservice.ErrSlugTaken) {
				writeJSON(w, http.StatusConflict, map[string]string{"error": "slug_taken"})
				return
			}
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, got)
	}
}

// getPageHandler returns one page by slug or W-ref.
func getPageHandler(deps *Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps.WikiService == nil {
			http.Error(w, "wiki service not wired", http.StatusServiceUnavailable)
			return
		}
		slug, err := resolveWikiRef(r.Context(), deps, chi.URLParam(r, "slug"))
		if err != nil {
			writeWikiResolveError(w, err)
			return
		}
		p, err := deps.WikiService.GetBySlug(r.Context(), slug)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, p)
	}
}

// getPageBacklinksHandler returns every page that links to the given slug or W-ref.
func getPageBacklinksHandler(deps *Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps.WikiService == nil {
			http.Error(w, "wiki service not wired", http.StatusServiceUnavailable)
			return
		}
		slug, err := resolveWikiRef(r.Context(), deps, chi.URLParam(r, "slug"))
		if err != nil {
			writeWikiResolveError(w, err)
			return
		}
		p, err := deps.WikiService.GetBySlug(r.Context(), slug)
		if err != nil {
			writeError(w, err)
			return
		}
		bl, err := deps.WikiService.Backlinks(r.Context(), p.ID)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"backlinks": bl})
	}
}

// deletePageHandler removes a page by slug or W-ref.
//
// Cascades to wiki_links via FK; the markdown mirror file is also removed
// (best-effort). 404 when the slug doesn't exist.
func deletePageHandler(deps *Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps.WikiService == nil {
			http.Error(w, "wiki service not wired", http.StatusServiceUnavailable)
			return
		}
		slug, err := resolveWikiRef(r.Context(), deps, chi.URLParam(r, "slug"))
		if err != nil {
			writeWikiResolveError(w, err)
			return
		}
		if err := deps.WikiService.Delete(r.Context(), slug); err != nil {
			writeError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// movePageRequest is the body of PATCH /pages/{slug}/move.
type movePageRequest struct {
	ParentID string `json:"parent_id"`
}

// movePageHandler moves a page under a new parent (or the root when
// parent_id is empty/null). 404 when the slug doesn't exist, 400
// when the move would create a cycle (parent is the page itself or
// one of its descendants).
func movePageHandler(deps *Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps.WikiService == nil {
			http.Error(w, "wiki service not wired", http.StatusServiceUnavailable)
			return
		}
		slug, err := resolveWikiRef(r.Context(), deps, chi.URLParam(r, "slug"))
		if err != nil {
			writeWikiResolveError(w, err)
			return
		}
		var in movePageRequest
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_json"})
			return
		}
		page, err := deps.WikiService.GetBySlug(r.Context(), slug)
		if err != nil {
			writeError(w, err)
			return
		}
		if err := deps.WikiService.Move(r.Context(), page.ID, in.ParentID); err != nil {
			writeError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// ----------------------------------------------------------------------------
// Search
// ----------------------------------------------------------------------------

// searchHandler runs a unified FTS5 query across pages/tasks/comments.
//
// Query params: q (required), type (optional, comma-separated),
// limit (optional, default 20 per type, max 100).
func searchHandler(deps *Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps.SearchService == nil {
			http.Error(w, "search service not wired", http.StatusServiceUnavailable)
			return
		}
		q := r.URL.Query().Get("q")
		types := parseSearchTypes(r.URL.Query().Get("type"))
		limit := parseLimitParam(r.URL.Query().Get("limit"), 20, 100)

		hits, err := deps.SearchService.Search(r.Context(), q, types, limit)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"hits":  hits,
			"total": len(hits),
		})
	}
}

// parseSearchTypes converts ?type=page,task into []search.Type. Empty → all.
func parseSearchTypes(s string) []search.Type {
	if s == "" {
		return nil
	}
	var out []search.Type
	for _, t := range strings.Split(s, ",") {
		switch strings.TrimSpace(t) {
		case "page":
			out = append(out, search.TypePage)
		case "task":
			out = append(out, search.TypeTask)
		case "comment":
			out = append(out, search.TypeComment)
		}
	}
	return out
}

// parseLimitParam parses an integer query param with min/max clamping.
func parseLimitParam(s string, def, maxVal int) int {
	if s == "" {
		return def
	}
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return def
		}
		n = n*10 + int(c-'0')
	}
	if n < 1 {
		return def
	}
	if n > maxVal {
		return maxVal
	}
	return n
}
