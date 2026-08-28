package api_test

// T81: agent-namespace wiki blocks API (REST mirror of the user-side
// /pages/{slug}/blocks surface).
//
// The blocks handlers (getPageBlocksHandler / putPageBlocksHandler) are
// namespace-agnostic: they resolve the {slug} URL parameter through
// resolveWikiRef, so both slug and W<N> references work. These tests pin:
//
//   - GET /agent/pages/{slug}/blocks returns the BlockView by slug and W<N>.
//   - PUT /agent/pages/{slug}/blocks replaces the block tree; GET shows
//     the new tree afterwards.
//   - Bad request bodies: not-JSON → 400 invalid_json, no blocks →
//     400 missing_blocks, garbage blocks → 400 invalid_blocks_json.
//   - 404 on an unknown slug and an unknown W-ref.
//   - Auth surface: no token → 401, a user orenda_session cookie does
//     NOT authorize (namespaces stay split), garbage bearer → 401.

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
	wikiservice "github.com/ramgml/orenda/internal/service/wiki"
)

// agentBlocksFixture: two pages created via the agent API (same shape
// as wikiRefFixture) so slug and W-ref paths are both exercised.
type agentBlocksFixture struct {
	*agentWikiFixture
	page1ID     string
	page1Number int
	page1Slug   string
	page2ID     string
	page2Number int
}

func newAgentBlocksFixture(t *testing.T) *agentBlocksFixture {
	fx := newAgentWikiFixture(t)

	rr := fx.agentReq(http.MethodPut, "/api/v1/agent/pages/blk-alpha", map[string]any{
		"title":      "Blocks Alpha",
		"content_md": "Alpha content",
	})
	require.Equal(t, http.StatusOK, rr.Code, "create alpha: %s", rr.Body.String())
	var p1 wiki.Page
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&p1))
	assert.Greater(t, p1.Number, 0, "created page must carry a number")

	rr = fx.agentReq(http.MethodPut, "/api/v1/agent/pages/blk-beta", map[string]any{
		"title":      "Blocks Beta",
		"content_md": "Beta content",
	})
	require.Equal(t, http.StatusOK, rr.Code, "create beta: %s", rr.Body.String())
	var p2 wiki.Page
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&p2))

	return &agentBlocksFixture{
		agentWikiFixture: fx,
		page1ID:          p1.ID,
		page1Number:      p1.Number,
		page1Slug:        p1.Slug,
		page2ID:          p2.ID,
		page2Number:      p2.Number,
	}
}

// agentGetBlocks sends GET /api/v1/agent/pages/{ref}/blocks with the
// agent bearer token.
func (fx *agentBlocksFixture) agentGetBlocks(ref string) *httptest.ResponseRecorder {
	return fx.agentReq(http.MethodGet, "/api/v1/agent/pages/"+ref+"/blocks", nil)
}

// agentPutBlocks sends PUT /api/v1/agent/pages/{ref}/blocks with the
// agent bearer token. A nil body sends an empty request body; blocks
// wrapped in a *bytes.Reader are sent verbatim.
func (fx *agentBlocksFixture) agentPutBlocks(ref string, blocks any) *httptest.ResponseRecorder {
	return fx.agentReq(http.MethodPut, "/api/v1/agent/pages/"+ref+"/blocks", blocks)
}

// blocksBySlug returns the wire shape of a two-block tree, mirroring
// the user-side tests in wiki_blocks_test.go.
func blocksBySlug() []map[string]any {
	return []map[string]any{
		{"id": "b1", "type": "paragraph", "content": []map[string]any{
			{"type": "text", "text": "Hello from agent blocks"},
		}},
		{"id": "b2", "type": "heading", "props": map[string]int{"level": 2}, "content": []map[string]any{
			{"type": "text", "text": "Section"},
		}},
	}
}

// TestAgentWikiBlocks_GetBySlugAndGetByWRef: GET returns the BlockView
// for both reference forms; markdown-format pages pass content through.
func TestAgentWikiBlocks_GetBySlugAndGetByWRef(t *testing.T) {
	t.Parallel()
	fx := newAgentBlocksFixture(t)

	// By slug: the page was created with content_md, so format is
	// markdown and the raw content passes through.
	rr := fx.agentGetBlocks(fx.page1Slug)
	require.Equal(t, http.StatusOK, rr.Code, "GET by slug: %s", rr.Body.String())
	var bv wikiservice.BlockView
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&bv))
	assert.Equal(t, "markdown", bv.Format)
	assert.Equal(t, "Alpha content", bv.ContentMD)

	// By W<N>: same page, same view.
	ref := fmt.Sprintf("W%d", fx.page1Number)
	rr = fx.agentGetBlocks(ref)
	require.Equal(t, http.StatusOK, rr.Code, "GET by %q: %s", ref, rr.Body.String())
	var bv2 wikiservice.BlockView
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&bv2))
	assert.Equal(t, "markdown", bv2.Format)
	assert.Equal(t, "Alpha content", bv2.ContentMD)

	// Case-insensitive reference: w<N> resolves too.
	rr = fx.agentGetBlocks(fmt.Sprintf("w%d", fx.page1Number))
	assert.Equal(t, http.StatusOK, rr.Code, "GET by lowercase ref: %s", rr.Body.String())
}

// TestAgentWikiBlocks_PutReplacesTree: PUT blocks replaces the tree;
// GET then returns the new tree with format=blocks.
func TestAgentWikiBlocks_PutReplacesTree(t *testing.T) {
	t.Parallel()
	fx := newAgentBlocksFixture(t)
	blocks := blocksBySlug()

	// PUT by slug.
	rr := fx.agentPutBlocks(fx.page1Slug, map[string]any{"blocks": blocks})
	require.Equal(t, http.StatusOK, rr.Code, "PUT by slug: %s", rr.Body.String())
	var page wiki.Page
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&page))
	assert.Equal(t, fx.page1ID, page.ID, "PUT returns the updated page")
	assert.Equal(t, "blocks", page.ContentFormat)

	// GET by slug shows the new tree.
	rr = fx.agentGetBlocks(fx.page1Slug)
	require.Equal(t, http.StatusOK, rr.Code, "GET after PUT: %s", rr.Body.String())
	var bv wikiservice.BlockView
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&bv))
	assert.Equal(t, "blocks", bv.Format)
	require.Len(t, bv.Blocks, 2)
	assert.Equal(t, "b1", bv.Blocks[0].ID)
	assert.Equal(t, "paragraph", bv.Blocks[0].Type)
	assert.Equal(t, "b2", bv.Blocks[1].ID)

	// PUT by W<N> replaces the tree again (W-ref PUT round-trip).
	replacement := []map[string]any{
		{"id": "c1", "type": "bulletListItem", "content": []map[string]any{
			{"type": "text", "text": "Replaced via W-ref"},
		}},
	}
	ref := fmt.Sprintf("W%d", fx.page1Number)
	rr = fx.agentPutBlocks(ref, map[string]any{"blocks": replacement})
	require.Equal(t, http.StatusOK, rr.Code, "PUT by %q: %s", ref, rr.Body.String())

	// GET by slug reflects the replacement — one block only.
	rr = fx.agentGetBlocks(fx.page1Slug)
	require.Equal(t, http.StatusOK, rr.Code)
	var bv2 wikiservice.BlockView
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&bv2))
	require.Len(t, bv2.Blocks, 1)
	assert.Equal(t, "c1", bv2.Blocks[0].ID)
	assert.Contains(t, string(bv2.Blocks[0].Content), "Replaced via W-ref")
}

// TestAgentWikiBlocks_PutBadBodies pins the 400 error taxonomy:
// invalid_json (body not JSON), missing_blocks (no blocks field or
// null), invalid_blocks_json (blocks not a valid block array).
func TestAgentWikiBlocks_PutBadBodies(t *testing.T) {
	t.Parallel()
	fx := newAgentBlocksFixture(t)

	// Body is not JSON at all → invalid_json.
	req := httptest.NewRequest(http.MethodPut,
		"/api/v1/agent/pages/"+fx.page1Slug+"/blocks",
		bytes.NewReader([]byte("not json at all")))
	req.Header.Set("Authorization", "Bearer "+fx.token)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	fx.router.ServeHTTP(rr, req)
	require.Equal(t, http.StatusBadRequest, rr.Code, "non-JSON body: %s", rr.Body.String())
	assert.Contains(t, rr.Body.String(), "invalid_json")

	// JSON without a blocks field → missing_blocks.
	rr = fx.agentPutBlocks(fx.page1Slug, map[string]any{"title": "no blocks here"})
	require.Equal(t, http.StatusBadRequest, rr.Code, "missing blocks: %s", rr.Body.String())
	assert.Contains(t, rr.Body.String(), "missing_blocks")

	// Explicit null blocks → missing_blocks.
	rr = fx.agentPutBlocks(fx.page1Slug, map[string]any{"blocks": nil})
	require.Equal(t, http.StatusBadRequest, rr.Code, "null blocks: %s", rr.Body.String())
	assert.Contains(t, rr.Body.String(), "missing_blocks")

	// blocks is a string, not an array → invalid_blocks_json.
	rr = fx.agentPutBlocks(fx.page1Slug, map[string]any{"blocks": "just a string"})
	require.Equal(t, http.StatusBadRequest, rr.Code, "string blocks: %s", rr.Body.String())
	assert.Contains(t, rr.Body.String(), "invalid_blocks_json")

	// Duplicate block IDs → 400 (ReplaceBlockTree validation).
	rr = fx.agentPutBlocks(fx.page1Slug, map[string]any{"blocks": []map[string]any{
		{"id": "x", "type": "paragraph"},
		{"id": "x", "type": "paragraph"},
	}})
	require.Equal(t, http.StatusBadRequest, rr.Code, "dup ids: %s", rr.Body.String())

	// Unknown block type → 400 (whitelist).
	rr = fx.agentPutBlocks(fx.page1Slug, map[string]any{"blocks": []map[string]any{
		{"id": "y", "type": "videoEmbed"},
	}})
	require.Equal(t, http.StatusBadRequest, rr.Code, "unknown type: %s", rr.Body.String())
}

// TestAgentWikiBlocks_UnknownRef404: unknown slug and unknown W-ref
// both return 404.
func TestAgentWikiBlocks_UnknownRef404(t *testing.T) {
	t.Parallel()
	fx := newAgentBlocksFixture(t)

	rr := fx.agentGetBlocks("no-such-page-blocks")
	require.Equal(t, http.StatusNotFound, rr.Code, "unknown slug: %s", rr.Body.String())
	assert.Contains(t, rr.Body.String(), "not_found")

	rr = fx.agentGetBlocks("W999999")
	require.Equal(t, http.StatusNotFound, rr.Code, "unknown W-ref: %s", rr.Body.String())
	assert.Contains(t, rr.Body.String(), "page W999999 not found")

	// PUT to an unknown slug 404s as well.
	rr = fx.agentPutBlocks("no-such-page-blocks", map[string]any{"blocks": blocksBySlug()})
	assert.Equal(t, http.StatusNotFound, rr.Code, "PUT unknown slug: %s", rr.Body.String())
}

// TestAgentWikiBlocks_RequiresAgentToken: the /agent/ blocks surface
// only accepts an agent bearer token; a user session cookie does not
// authorize it.
func TestAgentWikiBlocks_RequiresAgentToken(t *testing.T) {
	t.Parallel()
	fx := newAgentBlocksFixture(t)

	for _, method := range []string{http.MethodGet, http.MethodPut} {
		// No token at all.
		req := httptest.NewRequest(method, "/api/v1/agent/pages/"+fx.page1Slug+"/blocks", nil)
		rr := httptest.NewRecorder()
		fx.router.ServeHTTP(rr, req)
		assert.Equal(t, http.StatusUnauthorized, rr.Code, "%s without token", method)

		// User cookie is NOT an agent credential.
		req = httptest.NewRequest(method, "/api/v1/agent/pages/"+fx.page1Slug+"/blocks", nil)
		req.AddCookie(&http.Cookie{Name: "orenda_session", Value: fx.cookie})
		rr = httptest.NewRecorder()
		fx.router.ServeHTTP(rr, req)
		assert.Equal(t, http.StatusUnauthorized, rr.Code, "%s with user cookie", method)

		// Garbage bearer.
		req = httptest.NewRequest(method, "/api/v1/agent/pages/"+fx.page1Slug+"/blocks", nil)
		req.Header.Set("Authorization", "Bearer wrong-token")
		rr = httptest.NewRecorder()
		fx.router.ServeHTTP(rr, req)
		assert.Equal(t, http.StatusUnauthorized, rr.Code, "%s with garbage bearer", method)
	}
}
