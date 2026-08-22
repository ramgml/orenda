package api_test

// Phase 36: wiki page numbers — W-refs resolve on every slug-taking surface.
//
// The agent and user wiki REST handlers accept "W<N>" in the {slug}
// URL parameter and resolve it through wiki_pages.number. These tests
// pin:
//
//   - GET /pages/W42 resolves to the page with number 42.
//   - DELETE /pages/W42 resolves and deletes.
//   - PATCH /pages/W42/move resolves and moves.
//   - GET /pages/W42/backlinks resolves.
//   - PUT /pages/W42 (upsert) resolves and updates.
//   - POST /pages with slug "W42" is rejected (422 slug_conflicts_with_w_ref).
//   - Unknown W-ref returns 404 "page W999 not found".
//   - Case-insensitive: w42 works too.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ramgml/orenda/internal/domain/wiki"
)

// wikiRefFixture creates two pages via the agent API and returns their
// numbers and IDs.
type wikiRefFixture struct {
	*agentWikiFixture
	page1ID     string
	page1Number int
	page1Slug   string
	page2ID     string
	page2Number int
	page2Slug   string
}

func newWikiRefFixture(t *testing.T) *wikiRefFixture {
	fx := newAgentWikiFixture(t)

	// Create page 1.
	rr := fx.agentReq(http.MethodPut, "/api/v1/agent/pages/wref-alpha", map[string]any{
		"title":      "Alpha",
		"content_md": "Page alpha content",
	})
	require.Equal(t, http.StatusOK, rr.Code, "create alpha: %s", rr.Body.String())
	var p1 wiki.Page
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&p1))
	assert.Greater(t, p1.Number, 0, "created page must carry a number")

	// Create page 2.
	rr = fx.agentReq(http.MethodPut, "/api/v1/agent/pages/wref-beta", map[string]any{
		"title":      "Beta",
		"content_md": "Page beta content",
	})
	require.Equal(t, http.StatusOK, rr.Code, "create beta: %s", rr.Body.String())
	var p2 wiki.Page
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&p2))
	assert.Greater(t, p2.Number, 0)

	return &wikiRefFixture{
		agentWikiFixture: fx,
		page1ID:          p1.ID,
		page1Number:      p1.Number,
		page1Slug:        p1.Slug,
		page2ID:          p2.ID,
		page2Number:      p2.Number,
		page2Slug:        p2.Slug,
	}
}

// TestWikiRef_GetByWRef: GET /agent/pages/W<N> resolves the page.
func TestWikiRef_GetByWRef(t *testing.T) {
	fx := newWikiRefFixture(t)
	ref := fmt.Sprintf("W%d", fx.page1Number)

	rr := fx.agentReq(http.MethodGet, "/api/v1/agent/pages/"+ref, nil)
	require.Equal(t, http.StatusOK, rr.Code, "GET by %q: %s", ref, rr.Body.String())
	var got wiki.Page
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&got))
	assert.Equal(t, fx.page1ID, got.ID, "W<N> must resolve to the same page")
	assert.Equal(t, fx.page1Number, got.Number)
}

// TestWikiRef_GetByWRefCaseInsensitive: w42 works the same as W42.
func TestWikiRef_GetByWRefCaseInsensitive(t *testing.T) {
	fx := newWikiRefFixture(t)
	ref := fmt.Sprintf("w%d", fx.page1Number)

	rr := fx.agentReq(http.MethodGet, "/api/v1/agent/pages/"+ref, nil)
	require.Equal(t, http.StatusOK, rr.Code, "GET by lowercase %q: %s", ref, rr.Body.String())
	var got wiki.Page
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&got))
	assert.Equal(t, fx.page1ID, got.ID)
}

// TestWikiRef_DeleteByWRef: DELETE /agent/pages/W<N> resolves and deletes.
func TestWikiRef_DeleteByWRef(t *testing.T) {
	fx := newWikiRefFixture(t)
	ref := fmt.Sprintf("W%d", fx.page1Number)

	rr := fx.agentReq(http.MethodDelete, "/api/v1/agent/pages/"+ref, nil)
	require.Equal(t, http.StatusNoContent, rr.Code, "DELETE by %q: %s", ref, rr.Body.String())

	// Subsequent GET must 404.
	rr = fx.agentReq(http.MethodGet, "/api/v1/agent/pages/"+fx.page1Slug, nil)
	assert.Equal(t, http.StatusNotFound, rr.Code, "deleted page must 404")
}

// TestWikiRef_MoveByWRef: PATCH /agent/pages/W<N>/move resolves and moves.
func TestWikiRef_MoveByWRef(t *testing.T) {
	fx := newWikiRefFixture(t)
	ref := fmt.Sprintf("W%d", fx.page2Number)

	rr := fx.agentReq(http.MethodPatch, "/api/v1/agent/pages/"+ref+"/move", map[string]any{
		"parent_id": fx.page1ID,
	})
	require.Equal(t, http.StatusNoContent, rr.Code, "MOVE by %q: %s", ref, rr.Body.String())

	// Verify the move via GET.
	rr = fx.agentReq(http.MethodGet, "/api/v1/agent/pages/"+fx.page2Slug, nil)
	require.Equal(t, http.StatusOK, rr.Code)
	var got wiki.Page
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&got))
	assert.Equal(t, fx.page1ID, got.ParentID, "page must be reparented")
}

// TestWikiRef_BacklinksByWRef: GET /agent/pages/W<N>/backlinks resolves.
func TestWikiRef_BacklinksByWRef(t *testing.T) {
	fx := newWikiRefFixture(t)

	// Create a page that links to page 2 via [[wref-beta]].
	rr := fx.agentReq(http.MethodPut, "/api/v1/agent/pages/wref-gamma", map[string]any{
		"title":      "Gamma",
		"content_md": "See [[wref-beta]] for details.",
	})
	require.Equal(t, http.StatusOK, rr.Code, "create gamma: %s", rr.Body.String())

	ref := fmt.Sprintf("W%d", fx.page2Number)
	rr = fx.agentReq(http.MethodGet, "/api/v1/agent/pages/"+ref+"/backlinks", nil)
	require.Equal(t, http.StatusOK, rr.Code, "backlinks by %q: %s", ref, rr.Body.String())
	var bl struct {
		Backlinks []wiki.Page `json:"backlinks"`
	}
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&bl))
	require.Len(t, bl.Backlinks, 1)
	assert.Equal(t, "wref-gamma", bl.Backlinks[0].Slug)
}

// TestWikiRef_UpsertByWRef: PUT /agent/pages/W<N> resolves and updates.
func TestWikiRef_UpsertByWRef(t *testing.T) {
	fx := newWikiRefFixture(t)
	ref := fmt.Sprintf("W%d", fx.page1Number)

	rr := fx.agentReq(http.MethodPut, "/api/v1/agent/pages/"+ref, map[string]any{
		"title":      "Alpha Updated",
		"content_md": "Updated content",
	})
	require.Equal(t, http.StatusOK, rr.Code, "PUT by %q: %s", ref, rr.Body.String())
	var got wiki.Page
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&got))
	assert.Equal(t, fx.page1ID, got.ID, "PUT by W-ref must keep the page id")
	assert.Equal(t, "Alpha Updated", got.Title)
}

// TestWikiRef_SlugConflict422: POST with slug "W42" is rejected.
func TestWikiRef_SlugConflict422(t *testing.T) {
	fx := newAgentWikiFixture(t)

	// W42 in the URL resolves — if the page exists it's an update,
	// if not it's a 404. But slug "W42" in the body must be rejected.
	// The test for slug rejection is on POST /pages (user side) with
	// body slug "W42".
	req := httptest.NewRequest(http.MethodPost, "/api/v1/pages", mustMarshal(t, map[string]any{
		"slug":  "W42",
		"title": "Conflicting",
	}))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "orenda_session", Value: fx.cookie})
	rr := httptest.NewRecorder()
	fx.router.ServeHTTP(rr, req)
	assert.Equal(t, http.StatusUnprocessableEntity, rr.Code,
		"W<digits> slug must be rejected with 422")
	var body map[string]string
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&body))
	assert.Equal(t, "slug_conflicts_with_w_ref", body["error"])
}

// TestWikiRef_Unknown404: unknown W-ref returns "page W999 not found".
func TestWikiRef_Unknown404(t *testing.T) {
	fx := newAgentWikiFixture(t)

	rr := fx.agentReq(http.MethodGet, "/api/v1/agent/pages/W999999", nil)
	require.Equal(t, http.StatusNotFound, rr.Code, "body=%s", rr.Body.String())
	var body map[string]string
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&body))
	assert.Equal(t, "page W999999 not found", body["error"])
}

// mustMarshal is a test helper that marshals and fails on error.
func mustMarshal(t *testing.T, v any) *bytes.Reader {
	t.Helper()
	b, err := json.Marshal(v)
	require.NoError(t, err)
	return bytes.NewReader(b)
}
