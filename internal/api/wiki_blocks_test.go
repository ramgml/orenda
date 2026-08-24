package api_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// putBlocks sends PUT /api/v1/pages/{slug}/blocks with the given block array.
func putBlocks(t *testing.T, router http.Handler, cookie, slug string, blocks any) *httptest.ResponseRecorder {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"blocks": blocks})
	req := httptest.NewRequest(http.MethodPut, "/api/v1/pages/"+slug+"/blocks", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "orenda_session", Value: cookie})
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	return rr
}

// getBlocks sends GET /api/v1/pages/{slug}/blocks.
func getBlocks(t *testing.T, router http.Handler, cookie, slug string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/pages/"+slug+"/blocks", nil)
	req.AddCookie(&http.Cookie{Name: "orenda_session", Value: cookie})
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	return rr
}

func TestWikiBlocks_PutAndGetRoundTrip(t *testing.T) {
	t.Parallel()
	router, cookie := wikiRouter(t)

	// Create a page via markdown.
	createPage(t, router, cookie, mustJSON(t, map[string]string{
		"slug": "bt", "title": "BlockTest", "content_md": "initial",
	}))

	// PUT blocks.
	blocks := []map[string]any{
		{"id": "b1", "type": "paragraph", "content": []map[string]any{
			{"type": "text", "text": "Hello from blocks"},
		}},
		{"id": "b2", "type": "heading", "props": map[string]int{"level": 2}, "content": []map[string]any{
			{"type": "text", "text": "Section"},
		}},
	}
	rr := putBlocks(t, router, cookie, "bt", blocks)
	require.Equal(t, http.StatusOK, rr.Code, "PUT: %s", rr.Body.String())

	// GET blocks.
	rr = getBlocks(t, router, cookie, "bt")
	require.Equal(t, http.StatusOK, rr.Code)
	var bv map[string]any
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &bv))
	assert.Equal(t, "blocks", bv["format"])
	blkArr, ok := bv["blocks"].([]any)
	require.True(t, ok)
	require.Len(t, blkArr, 2)

	// Verify content via GET page.
	req := httptest.NewRequest(http.MethodGet, "/api/v1/pages/bt", nil)
	req.AddCookie(&http.Cookie{Name: "orenda_session", Value: cookie})
	rr2 := httptest.NewRecorder()
	router.ServeHTTP(rr2, req)
	require.Equal(t, http.StatusOK, rr2.Code)
	var page map[string]any
	require.NoError(t, json.Unmarshal(rr2.Body.Bytes(), &page))
	assert.Equal(t, "blocks", page["content_format"])
	assert.Contains(t, page["content_md"], "Hello from blocks")
	assert.Contains(t, page["content_md"], "Section")
}

func TestWikiBlocks_GetLegacyMarkdown(t *testing.T) {
	t.Parallel()
	router, cookie := wikiRouter(t)

	createPage(t, router, cookie, mustJSON(t, map[string]string{
		"slug": "legacy-blk", "title": "Legacy", "content_md": "# Title",
	}))

	rr := getBlocks(t, router, cookie, "legacy-blk")
	require.Equal(t, http.StatusOK, rr.Code)
	var bv map[string]any
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &bv))
	assert.Equal(t, "markdown", bv["format"])
	assert.Equal(t, "# Title", bv["content_md"])
	assert.Nil(t, bv["blocks"])
}

func TestWikiBlocks_PutInvalidBlocks(t *testing.T) {
	t.Parallel()
	router, cookie := wikiRouter(t)

	createPage(t, router, cookie, mustJSON(t, map[string]string{
		"slug": "bad-blk", "title": "Bad Blocks",
	}))

	// Duplicate IDs.
	dupBlocks := []map[string]any{
		{"id": "x", "type": "paragraph"},
		{"id": "x", "type": "paragraph"},
	}
	rr := putBlocks(t, router, cookie, "bad-blk", dupBlocks)
	assert.Equal(t, http.StatusBadRequest, rr.Code)

	// Unknown type.
	badType := []map[string]any{
		{"id": "y", "type": "videoEmbed"},
	}
	rr = putBlocks(t, router, cookie, "bad-blk", badType)
	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestWikiBlocks_PutMissingBlocks(t *testing.T) {
	t.Parallel()
	router, cookie := wikiRouter(t)

	createPage(t, router, cookie, mustJSON(t, map[string]string{
		"slug": "miss-blk", "title": "Miss Blocks",
	}))

	// Empty body.
	body, _ := json.Marshal(map[string]any{"blocks": nil})
	req := httptest.NewRequest(http.MethodPut, "/api/v1/pages/miss-blk/blocks", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "orenda_session", Value: cookie})
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestWikiBlocks_PutMissingSlug(t *testing.T) {
	t.Parallel()
	router, cookie := wikiRouter(t)

	// PUT blocks for non-existent slug.
	rr := putBlocks(t, router, cookie, "nonexistent-blocks", []map[string]any{
		{"id": "b1", "type": "paragraph"},
	})
	assert.Equal(t, http.StatusNotFound, rr.Code)
}

func TestWikiBlocks_PutMarkdownRoundTrip(t *testing.T) {
	t.Parallel()
	router, cookie := wikiRouter(t)

	// Create with blocks.
	createPage(t, router, cookie, mustJSON(t, map[string]string{
		"slug": "md-rt", "title": "MD RT",
	}))
	putBlocks(t, router, cookie, "md-rt", []map[string]any{
		{"id": "b1", "type": "paragraph", "content": []map[string]any{
			{"type": "text", "text": "block text"},
		}},
	})

	// Save via markdown — should switch back.
	putBody, _ := json.Marshal(map[string]string{"title": "MD RT", "content_md": "# markdown content"})
	req := httptest.NewRequest(http.MethodPut, "/api/v1/pages/md-rt", bytes.NewReader(putBody))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "orenda_session", Value: cookie})
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)

	// Verify format switched back.
	rr = getBlocks(t, router, cookie, "md-rt")
	require.Equal(t, http.StatusOK, rr.Code)
	var bv map[string]any
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &bv))
	assert.Equal(t, "markdown", bv["format"])
	assert.Contains(t, bv["content_md"], "markdown content")
}
