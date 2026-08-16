// Phase 29.3: tool-level tests for the wiki/search MCP surface.
//
// RegisterOrendaTools is pointed at an httptest server that records
// the request shape; tools are invoked through the real JSON-RPC
// handler so name → endpoint mapping, verbs, and parameter encoding
// are pinned end-to-end.
package mcp

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// recordedRequest captures the last request the fake Orenda server
// received.
type recordedRequest struct {
	method      string
	path        string
	escapedPath string
	query       url.Values
	body        map[string]any
	authHdr     string
}

// newToolServer wires a MCP server with the Orenda tools registered
// against a recording httptest backend.
func newToolServer(t *testing.T, responder func(w http.ResponseWriter, r *http.Request)) (*Server, *recordedRequest) {
	t.Helper()
	rec := &recordedRequest{}
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.method = r.Method
		rec.path = r.URL.Path
		rec.escapedPath = r.URL.EscapedPath()
		rec.query = r.URL.Query()
		rec.authHdr = r.Header.Get("Authorization")
		if r.Body != nil {
			_ = json.NewDecoder(r.Body).Decode(&rec.body)
		}
		if responder != nil {
			responder(w, r)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	t.Cleanup(backend.Close)

	srv := NewServer("orenda-test", "0.0.0")
	RegisterOrendaTools(srv, ServerConfig{
		OrendaBaseURL: backend.URL,
		AgentToken:    "tok-abc",
	})
	return srv, rec
}

// callTool invokes a registered tool through the JSON-RPC surface.
func callTool(t *testing.T, srv *Server, name string, args map[string]any) map[string]any {
	t.Helper()
	rawArgs, err := json.Marshal(args)
	require.NoError(t, err)
	raw := fmt.Sprintf(
		`{"jsonrpc":"2.0","id":42,"method":"tools/call","params":{"name":%q,"arguments":%s}}`,
		name, rawArgs)
	resp := call(t, srv, raw)
	result, ok := resp["result"].(map[string]any)
	require.True(t, ok, "tool %s should succeed, got %v", name, resp)
	return result
}

func TestOrendaTools_ListIncludesWikiAndSearch(t *testing.T) {
	srv, _ := newToolServer(t, nil)
	resp := call(t, srv, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	tools := resp["result"].(map[string]any)["tools"].([]any)
	names := map[string]bool{}
	for _, tool := range tools {
		names[tool.(map[string]any)["name"].(string)] = true
	}
	for _, want := range []string{
		// Pre-existing seven.
		"orenda_me", "orenda_list_tasks", "orenda_claim", "orenda_release",
		"orenda_submit", "orenda_context", "orenda_await",
		// Phase 29.3 additions.
		"orenda_pages_list", "orenda_pages_get", "orenda_pages_save",
		"orenda_pages_delete", "orenda_pages_move", "orenda_search",
	} {
		assert.True(t, names[want], "tools/list must include %s", want)
	}
}

func TestOrendaTools_PagesSaveSendsPUT(t *testing.T) {
	srv, rec := newToolServer(t, nil)
	callTool(t, srv, "orenda_pages_save", map[string]any{
		"slug": "guide", "title": "Guide", "content_md": "# Hi", "parent_id": "p-1",
	})
	assert.Equal(t, http.MethodPut, rec.method)
	assert.Equal(t, "/api/v1/agent/pages/guide", rec.path)
	assert.Equal(t, "Bearer tok-abc", rec.authHdr)
	assert.Equal(t, "Guide", rec.body["title"])
	assert.Equal(t, "# Hi", rec.body["content_md"])
	assert.Equal(t, "p-1", rec.body["parent_id"])
}

func TestOrendaTools_PagesSaveRequiresSlugAndTitle(t *testing.T) {
	srv, rec := newToolServer(t, nil)
	raw := `{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"orenda_pages_save","arguments":{"slug":""}}}`
	resp := call(t, srv, raw)
	// Tool-level validation failure surfaces as a JSON-RPC error,
	// and the backend must NOT be hit.
	assert.NotNil(t, resp["error"], "missing title must be a tool error")
	assert.Empty(t, rec.method, "backend must not be called on invalid input")
}

func TestOrendaTools_PagesDeleteSendsDELETE(t *testing.T) {
	srv, rec := newToolServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	res := callTool(t, srv, "orenda_pages_delete", map[string]any{"slug": "old-page"})
	assert.Equal(t, http.MethodDelete, rec.method)
	assert.Equal(t, "/api/v1/agent/pages/old-page", rec.path)
	// 204 with empty body → the tool reports the status code.
	content := res["content"].([]any)[0].(map[string]any)
	assert.Contains(t, content["text"], "204")
}

func TestOrendaTools_PagesMoveSendsPATCH(t *testing.T) {
	srv, rec := newToolServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	callTool(t, srv, "orenda_pages_move", map[string]any{"slug": "child", "parent_id": "parent-9"})
	assert.Equal(t, http.MethodPatch, rec.method)
	assert.Equal(t, "/api/v1/agent/pages/child/move", rec.path)
	assert.Equal(t, "parent-9", rec.body["parent_id"])
}

func TestOrendaTools_PagesGetEscapesSlug(t *testing.T) {
	srv, rec := newToolServer(t, nil)
	callTool(t, srv, "orenda_pages_get", map[string]any{"slug": "with space"})
	assert.Equal(t, http.MethodGet, rec.method)
	// r.URL.Path is decoded server-side; the wire form is what matters.
	assert.Equal(t, "/api/v1/agent/pages/with%20space", rec.escapedPath,
		"slug must be path-escaped on the wire")
}

func TestOrendaTools_SearchEncodesParams(t *testing.T) {
	srv, rec := newToolServer(t, nil)
	callTool(t, srv, "orenda_search", map[string]any{
		"query": "hello world", "type": "page", "limit": 5,
	})
	assert.Equal(t, "/api/v1/agent/search", rec.path)
	assert.Equal(t, "hello world", rec.query.Get("q"))
	assert.Equal(t, "page", rec.query.Get("type"))
	assert.Equal(t, "5", rec.query.Get("limit"))
}

func TestOrendaTools_AwaitUsesAgentNamespace(t *testing.T) {
	// Pins the Phase 29.3 fix: the pre-29.3 tool posted to the
	// user-side /api/v1/events/await, which 401s on agent tokens.
	srv, rec := newToolServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	callTool(t, srv, "orenda_await", map[string]any{"timeout_s": 1})
	assert.Equal(t, "/api/v1/agent/events/await", rec.path)
	assert.Equal(t, "Bearer tok-abc", rec.authHdr)
	assert.Equal(t, float64(1), rec.body["timeout_s"])
}
