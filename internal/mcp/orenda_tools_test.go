// Phase 29.3: tool-level tests for the wiki/search MCP surface.
//
// RegisterOrendaTools is pointed at an httptest server that records
// the request shape; tools are invoked through the real JSON-RPC
// handler so name → endpoint mapping, verbs, and parameter encoding
// are pinned end-to-end.
package mcp

import (
	"bytes"
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
		// T80: page attachment tools.
		"orenda_page_attachment_upload", "orenda_page_attachments_list",
		"orenda_courses_list", "orenda_study_propose",
		// Phase 33.1 addition.
		"orenda_task_propose",
		// T72 addition.
		"orenda_list_projects",
		// T82: page blocks get/save tool.
		"orenda_pages_blocks",
	} {
		assert.True(t, names[want], "tools/list must include %s", want)
	}
}

// T72: the projects tool is a bare GET on the agent namespace —
// no arguments, no query string, response passed through verbatim.
func TestOrendaTools_ListProjectsSendsGET(t *testing.T) {
	srv, rec := newToolServer(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"projects":[{"id":"p-1","name":"Agent Proj"}]}`))
	})
	result := callTool(t, srv, "orenda_list_projects", map[string]any{})
	assert.Equal(t, http.MethodGet, rec.method)
	assert.Equal(t, "/api/v1/agent/projects", rec.path)
	assert.Equal(t, "Bearer tok-abc", rec.authHdr)
	assert.Empty(t, rec.query.Encode(), "no arguments must produce a clean URL")
	content, ok := result["content"].([]any)
	require.True(t, ok, "result must carry content blocks, got %v", result)
	require.Len(t, content, 1)
	text := content[0].(map[string]any)["text"].(string)
	assert.Contains(t, text, `"Agent Proj"`, "response body must pass through")
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

// ------------------------------------------------------------------
// T82: orenda_pages_blocks — one tool, two modes. Absence of the
// `blocks` argument reads the BlockView (GET); presence replaces the
// whole tree (PUT). Both hit the same agent-namespace path, and the
// slug (or W-ref) must stay path-escaped like the other page tools.
// ------------------------------------------------------------------

func TestOrendaTools_PagesBlocksGetSendsGET(t *testing.T) {
	srv, rec := newToolServer(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"format":"blocks","blocks":[
			{"id":"b1","type":"paragraph","content":[{"type":"text","text":"Hello"}]}
		]}`))
	})
	result := callTool(t, srv, "orenda_pages_blocks", map[string]any{"slug": "W15"})
	assert.Equal(t, http.MethodGet, rec.method)
	assert.Equal(t, "/api/v1/agent/pages/W15/blocks", rec.path)
	assert.Equal(t, "Bearer tok-abc", rec.authHdr)
	assert.Nil(t, rec.body, "read mode must send no request body")
	// The BlockView passes through verbatim.
	content, ok := result["content"].([]any)
	require.True(t, ok, "result must carry content blocks, got %v", result)
	require.Len(t, content, 1)
	text := content[0].(map[string]any)["text"].(string)
	assert.Contains(t, text, `"format": "blocks"`, "BlockView must pass through")
	assert.Contains(t, text, "Hello")
}

func TestOrendaTools_PagesBlocksPutSendsWrappedTree(t *testing.T) {
	srv, rec := newToolServer(t, nil)
	callTool(t, srv, "orenda_pages_blocks", map[string]any{
		"slug": "guide",
		"blocks": []any{
			map[string]any{
				"id": "b1", "type": "paragraph",
				"content": []any{map[string]any{"type": "text", "text": "Hello"}},
			},
			map[string]any{"id": "b2", "type": "heading", "props": map[string]any{"level": 2}},
		},
	})
	assert.Equal(t, http.MethodPut, rec.method)
	assert.Equal(t, "/api/v1/agent/pages/guide/blocks", rec.path)
	assert.Equal(t, "Bearer tok-abc", rec.authHdr)
	// The tree rides in the {"blocks": [...]} envelope the server parses.
	blocks, ok := rec.body["blocks"].([]any)
	require.True(t, ok, "body must wrap the tree as {blocks: [...]}, got %v", rec.body)
	require.Len(t, blocks, 2)
	b1 := blocks[0].(map[string]any)
	assert.Equal(t, "b1", b1["id"])
	assert.Equal(t, "paragraph", b1["type"])
	assert.Equal(t, []any{map[string]any{"type": "text", "text": "Hello"}}, b1["content"])
	b2 := blocks[1].(map[string]any)
	assert.Equal(t, map[string]any{"level": float64(2)}, b2["props"])
}

func TestOrendaTools_PagesBlocksPutEscapesSlug(t *testing.T) {
	srv, rec := newToolServer(t, nil)
	callTool(t, srv, "orenda_pages_blocks", map[string]any{
		"slug":   "with space",
		"blocks": []any{map[string]any{"id": "b1", "type": "paragraph"}},
	})
	assert.Equal(t, "/api/v1/agent/pages/with%20space/blocks", rec.escapedPath,
		"slug must be path-escaped on the wire")
}

// TestOrendaTools_PagesBlocksServerError — 400/404 from the backend
// must surface with status line + body text, not be swallowed.
func TestOrendaTools_PagesBlocksServerError(t *testing.T) {
	for _, tc := range []struct {
		name       string
		status     int
		body       string
		args       map[string]any
		wantSubstr []string
	}{
		{
			name:       "read: unknown page",
			status:     http.StatusNotFound,
			body:       `{"error":"not_found"}`,
			args:       map[string]any{"slug": "nope"},
			wantSubstr: []string{"404 Not Found", `{"error":"not_found"}`},
		},
		{
			name:   "write: invalid blocks",
			status: http.StatusBadRequest,
			body:   `{"error":"invalid_blocks_json"}`,
			args: map[string]any{
				"slug":   "guide",
				"blocks": []any{map[string]any{"id": "b1", "type": "paragraph"}},
			},
			wantSubstr: []string{"400 Bad Request", `{"error":"invalid_blocks_json"}`},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv, _ := newToolServer(t, func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			})
			resp := call(t, srv, fmt.Sprintf(
				`{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"orenda_pages_blocks","arguments":%s}}`,
				mustMarshal(t, tc.args)))
			errObj, ok := resp["error"].(map[string]any)
			require.True(t, ok, "tool should return an error, got %v", resp)
			msg, _ := errObj["message"].(string)
			assert.Contains(t, msg, "orenda_pages_blocks:")
			for _, want := range tc.wantSubstr {
				assert.Contains(t, msg, want)
			}
		})
	}
}

func TestOrendaTools_PageAttachmentUploadSendsMultipart(t *testing.T) {
	srv, rec := newToolServer(t, nil)
	callTool(t, srv, "orenda_page_attachment_upload", map[string]any{
		"slug":         "vpn setup",
		"filename":     "topology.png",
		"content_utf8": "png-bytes",
		"mime":         "image/png",
	})
	assert.Equal(t, http.MethodPost, rec.method)
	assert.Equal(t, "/api/v1/agent/pages/vpn%20setup/attachments", rec.escapedPath,
		"slug must be path-escaped on the wire")
	assert.Equal(t, "Bearer tok-abc", rec.authHdr)
	assert.Contains(t, rec.escapedPath, "/attachments", "must hit the agent attachment route")
}

func TestOrendaTools_PageAttachmentUploadValidatesInput(t *testing.T) {
	srv, rec := newToolServer(t, nil)
	raw := `{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"orenda_page_attachment_upload","arguments":{"slug":"x"}}}`
	resp := call(t, srv, raw)
	assert.NotNil(t, resp["error"], "missing filename must be a tool error")
	assert.Empty(t, rec.method, "backend must not be called on invalid input")

	raw = `{"jsonrpc":"2.0","id":8,"method":"tools/call","params":{"name":"orenda_page_attachment_upload","arguments":{"slug":"x","filename":"f.png","content_b64":"!!!"}}}`
	resp = call(t, srv, raw)
	assert.NotNil(t, resp["error"], "invalid base64 must be a tool error")
	assert.Empty(t, rec.method, "backend must not be called on invalid base64")
}

func TestOrendaTools_PageAttachmentsListSendsGET(t *testing.T) {
	srv, rec := newToolServer(t, nil)
	callTool(t, srv, "orenda_page_attachments_list", map[string]any{"slug": "W15"})
	assert.Equal(t, http.MethodGet, rec.method)
	assert.Equal(t, "/api/v1/agent/pages/W15/attachments", rec.path)
	assert.Equal(t, "Bearer tok-abc", rec.authHdr)
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

// TestOrendaTools_CoursesListEncodesStatus — Phase 31.8: the
// optional ?status= filter is the only knob the planner turns.
// Pin: status present → query string carries it; absent → path
// is the bare agent namespace.
func TestOrendaTools_CoursesListEncodesStatus(t *testing.T) {
	srv, rec := newToolServer(t, nil)

	// With status.
	callTool(t, srv, "orenda_courses_list", map[string]any{"status": "active"})
	assert.Equal(t, "/api/v1/agent/courses", rec.path)
	assert.Equal(t, "active", rec.query.Get("status"))
}

// TestOrendaTools_CoursesListNoStatus — omitting status keeps the
// URL clean (no trailing "?").
func TestOrendaTools_CoursesListNoStatus(t *testing.T) {
	srv, rec := newToolServer(t, nil)
	callTool(t, srv, "orenda_courses_list", map[string]any{})
	assert.Equal(t, "/api/v1/agent/courses", rec.path)
	assert.Empty(t, rec.query.Encode(),
		"empty status must not produce a trailing ?status= on the URL")
}

// TestOrendaTools_StudyProposeSendsPOST — Phase 31.8: the body is
// POSTed to /api/v1/agent/study-proposals with title/target_date
// required and course_id/body_md optional.
func TestOrendaTools_StudyProposeSendsPOST(t *testing.T) {
	srv, rec := newToolServer(t, nil)
	callTool(t, srv, "orenda_study_propose", map[string]any{
		"course_id":   "c-1",
		"title":       "Read chapter 5",
		"body_md":     "rust-book chapter 5",
		"target_date": "2099-08-17",
	})
	assert.Equal(t, http.MethodPost, rec.method)
	assert.Equal(t, "/api/v1/agent/study-proposals", rec.path)
	assert.Equal(t, "c-1", rec.body["course_id"])
	assert.Equal(t, "Read chapter 5", rec.body["title"])
	assert.Equal(t, "rust-book chapter 5", rec.body["body_md"])
	assert.Equal(t, "2099-08-17", rec.body["target_date"])
}

// TestOrendaTools_StudyProposeOmitsOptionalFields — when the
// planner doesn't supply course_id or body_md, those keys must
// not appear in the body (avoid sending empty strings the server
// would store as-is).
func TestOrendaTools_StudyProposeOmitsOptionalFields(t *testing.T) {
	srv, rec := newToolServer(t, nil)
	callTool(t, srv, "orenda_study_propose", map[string]any{
		"title":       "Free-standing",
		"target_date": "2099-08-17",
	})
	_, hasCourse := rec.body["course_id"]
	_, hasBody := rec.body["body_md"]
	assert.False(t, hasCourse, "course_id must not appear when caller omitted it")
	assert.False(t, hasBody, "body_md must not appear when caller omitted it")
}

// TestOrendaTools_StudyProposeRequiresTitleAndDate — the tool
// refuses inputs that the server would 400 on; the planner sees a
// readable error instead of a useless HTTP traceback.
func TestOrendaTools_StudyProposeRequiresTitleAndDate(t *testing.T) {
	srv, rec := newToolServer(t, nil)
	for _, tc := range []struct {
		name string
		args map[string]any
		want string
	}{
		{"empty title", map[string]any{"title": "", "target_date": "2099-08-17"},
			"title and target_date are required"},
		{"empty date", map[string]any{"title": "Read chapter 5", "target_date": ""},
			"title and target_date are required"},
		{"both empty", map[string]any{}, "title and target_date are required"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resp := call(t, srv, fmt.Sprintf(
				`{"jsonrpc":"2.0","id":99,"method":"tools/call","params":{"name":"orenda_study_propose","arguments":%s}}`,
				mustMarshal(t, tc.args)))
			errObj, ok := resp["error"].(map[string]any)
			require.True(t, ok, "tool should return an error, got %v", resp)
			// JSON-RPC's error message is the static "tool error";
			// the actual reason lands in the "data" field (the
			// original error string).
			data, _ := errObj["data"].(string)
			assert.Contains(t, data, tc.want)
		})
	}
	// No HTTP call should have hit the fake server.
	assert.Empty(t, rec.path, "validation should happen before the network call")
}

// TestOrendaTools_TaskProposeSendsPOST — Phase 33.1: the body is
// POSTed to /api/v1/agent/tasks with project_id/title/description_md
// required and priority/blocked_by/parent_task_id optional.
func TestOrendaTools_TaskProposeSendsPOST(t *testing.T) {
	srv, rec := newToolServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"t-1","status":"backlog","awaiting":"human"}`))
	})
	callTool(t, srv, "orenda_task_propose", map[string]any{
		"project_id":     "p-1",
		"title":          "Write the report",
		"description_md": "# Why\n\nAgents couldn't create tasks.",
		"priority":       "high",
		"blocked_by":     []any{"t-0"},
		"parent_task_id": "t-parent",
	})
	assert.Equal(t, http.MethodPost, rec.method)
	assert.Equal(t, "/api/v1/agent/tasks", rec.path)
	assert.Equal(t, "Bearer tok-abc", rec.authHdr)
	assert.Equal(t, "p-1", rec.body["project_id"])
	assert.Equal(t, "Write the report", rec.body["title"])
	assert.Equal(t, "# Why\n\nAgents couldn't create tasks.", rec.body["description_md"])
	assert.Equal(t, "high", rec.body["priority"])
	assert.Equal(t, []any{"t-0"}, rec.body["blocked_by"])
	assert.Equal(t, "t-parent", rec.body["parent_task_id"])
}

// TestOrendaTools_TaskProposeOmitsOptionalFields — optional keys must
// not appear in the body when the caller omitted them.
func TestOrendaTools_TaskProposeOmitsOptionalFields(t *testing.T) {
	srv, rec := newToolServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"t-1"}`))
	})
	callTool(t, srv, "orenda_task_propose", map[string]any{
		"project_id": "p-1", "title": "T", "description_md": "D",
	})
	for _, k := range []string{"priority", "blocked_by", "parent_task_id"} {
		_, has := rec.body[k]
		assert.False(t, has, "%s must not appear when caller omitted it", k)
	}
}

// TestOrendaTools_TaskProposeRequiresFields — the tool refuses inputs
// the server would 400 on, before any network call.
func TestOrendaTools_TaskProposeRequiresFields(t *testing.T) {
	srv, rec := newToolServer(t, nil)
	for _, tc := range []struct {
		name string
		args map[string]any
	}{
		{"missing project_id", map[string]any{"title": "T", "description_md": "D"}},
		{"missing title", map[string]any{"project_id": "p-1", "description_md": "D"}},
		{"missing description", map[string]any{"project_id": "p-1", "title": "T"}},
		{"blank description", map[string]any{"project_id": "p-1", "title": "T", "description_md": "  "}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resp := call(t, srv, fmt.Sprintf(
				`{"jsonrpc":"2.0","id":98,"method":"tools/call","params":{"name":"orenda_task_propose","arguments":%s}}`,
				mustMarshal(t, tc.args)))
			errObj, ok := resp["error"].(map[string]any)
			require.True(t, ok, "tool should return an error, got %v", resp)
			data, _ := errObj["data"].(string)
			assert.Contains(t, data, "project_id, title and description_md are required")
		})
	}
	assert.Empty(t, rec.path, "validation should happen before the network call")
}

// ------------------------------------------------------------------
// Task 48: URL-escape path parameters. Every task-id-bearing tool
// must use url.PathEscape so special characters arrive intact.
// With the T-prefix form ("T42") the path is clean; the test pins
// that the tool forwards the ref without mangling.
// ------------------------------------------------------------------

func TestOrendaTools_ContextSendsGET(t *testing.T) {
	srv, rec := newToolServer(t, nil)
	callTool(t, srv, "orenda_context", map[string]any{"task_id": "T42"})
	assert.Equal(t, http.MethodGet, rec.method)
	assert.Equal(t, "/api/v1/agent/tasks/T42/context", rec.escapedPath)
}

func TestOrendaTools_ClaimSendsPOST(t *testing.T) {
	srv, rec := newToolServer(t, nil)
	callTool(t, srv, "orenda_claim", map[string]any{"task_id": "T42"})
	assert.Equal(t, http.MethodPost, rec.method)
	assert.Equal(t, "/api/v1/agent/tasks/T42/claim", rec.escapedPath)
}

func TestOrendaTools_ReleaseSendsPOST(t *testing.T) {
	srv, rec := newToolServer(t, nil)
	callTool(t, srv, "orenda_release", map[string]any{"task_id": "T42"})
	assert.Equal(t, http.MethodPost, rec.method)
	assert.Equal(t, "/api/v1/agent/tasks/T42/release", rec.escapedPath)
}

func TestOrendaTools_SubmitSendsPOST(t *testing.T) {
	srv, rec := newToolServer(t, nil)
	callTool(t, srv, "orenda_submit", map[string]any{"task_id": "T42"})
	assert.Equal(t, http.MethodPost, rec.method)
	assert.Equal(t, "/api/v1/agent/tasks/T42/submit", rec.escapedPath)
}

func TestOrendaTools_TaskUpdateSendsPATCH(t *testing.T) {
	srv, rec := newToolServer(t, nil)
	callTool(t, srv, "orenda_task_update", map[string]any{
		"task_id": "T42", "title": "new title",
	})
	assert.Equal(t, http.MethodPatch, rec.method)
	assert.Equal(t, "/api/v1/agent/tasks/T42", rec.escapedPath)
}

func TestOrendaTools_TaskRetractSendsDELETE(t *testing.T) {
	srv, rec := newToolServer(t, nil)
	callTool(t, srv, "orenda_task_retract", map[string]any{"task_id": "T42"})
	assert.Equal(t, http.MethodDelete, rec.method)
	assert.Equal(t, "/api/v1/agent/tasks/T42", rec.escapedPath)
}

// Regression: a bare UUID must not be double-escaped.
func TestOrendaTools_ContextUUIDUnchanged(t *testing.T) {
	srv, rec := newToolServer(t, nil)
	uuid := "01a02791-45d6-7984-b770-0ba9204f74a5"
	callTool(t, srv, "orenda_context", map[string]any{"task_id": uuid})
	assert.Equal(t, "/api/v1/agent/tasks/"+uuid+"/context", rec.escapedPath)
}

// Regression: a numeric id must not be touched.
func TestOrendaTools_ContextNumericID(t *testing.T) {
	srv, rec := newToolServer(t, nil)
	callTool(t, srv, "orenda_context", map[string]any{"task_id": "35"})
	assert.Equal(t, "/api/v1/agent/tasks/35/context", rec.escapedPath)
}

// mustMarshal is a tiny test helper that fails fast on JSON errors.
func mustMarshal(t *testing.T, v any) string {
	t.Helper()
	raw, err := json.Marshal(v)
	require.NoError(t, err)
	return string(raw)
}

// ------------------------------------------------------------------
// Task 73: HTTP error detail propagation. A non-2xx from the Orenda
// server must reach the agent with status + body, and the tool name
// must prefix the JSON-RPC message (many clients render only
// `message`, never `data`).
// ------------------------------------------------------------------

// TestOrendaTools_ServerErrorShowsStatusAndBody — fake backend
// answers 404/422 with a JSON body; the tool error must carry the
// status line and the body verbatim.
func TestOrendaTools_ServerErrorShowsStatusAndBody(t *testing.T) {
	for _, tc := range []struct {
		name       string
		status     int
		body       string
		wantSubstr []string
	}{
		{
			name:       "not found",
			status:     http.StatusNotFound,
			body:       `{"error":"not_found"}`,
			wantSubstr: []string{"404 Not Found", `{"error":"not_found"}`},
		},
		{
			name:       "unprocessable",
			status:     http.StatusUnprocessableEntity,
			body:       `{"error":"invalid_project"}`,
			wantSubstr: []string{"422 Unprocessable Entity", `{"error":"invalid_project"}`},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv, _ := newToolServer(t, func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			})
			resp := call(t, srv, fmt.Sprintf(
				`{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"orenda_task_propose","arguments":%s}}`,
				mustMarshal(t, map[string]any{
					"project_id": "nope", "title": "T", "description_md": "D",
				})))
			errObj, ok := resp["error"].(map[string]any)
			require.True(t, ok, "tool should return an error, got %v", resp)
			// The reason is in `message` (what clients render)...
			msg, _ := errObj["message"].(string)
			assert.Contains(t, msg, "orenda_task_propose:")
			for _, want := range tc.wantSubstr {
				assert.Contains(t, msg, want)
			}
			// ...and duplicated in `data` (what data-aware clients read).
			data, _ := errObj["data"].(string)
			assert.Equal(t, msg, data)
			// The static "tool error" line is gone.
			assert.NotEqual(t, "tool error", msg)
		})
	}
}

// TestOrendaTools_ServerErrorBodyTruncated — an oversized error
// body is cut at maxErrBody so an HTML 500 page cannot flood the
// agent's context.
// T140: the list tool forwards `project` verbatim to the agent
// namespace — the server resolves number, P-number and UUID forms.
func TestOrendaTools_ListTasksProjectScopesQuery(t *testing.T) {
	for _, tc := range []struct {
		name     string
		args     map[string]any
		wantProj string
	}{
		{"number form", map[string]any{"project": "7"}, "7"},
		{"P-number form", map[string]any{"project": "P7"}, "P7"},
		{"uppercase P-number", map[string]any{"project": "p7"}, "p7"},
		{"uuid form", map[string]any{"project": "019934b2-1234-7abc-89ef-0123456789ab"}, "019934b2-1234-7abc-89ef-0123456789ab"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv, rec := newToolServer(t, nil)
			callTool(t, srv, "orenda_list_tasks", tc.args)
			assert.Equal(t, "/api/v1/agent/tasks", rec.path)
			assert.Equal(t, tc.wantProj, rec.query.Get("project"))
		})
	}
}

// T140: without a project argument the query must stay clean —
// no empty project= parameter on the wire.
func TestOrendaTools_ListTasksNoProjectKeepsQueryClean(t *testing.T) {
	srv, rec := newToolServer(t, nil)
	callTool(t, srv, "orenda_list_tasks", map[string]any{})
	assert.Equal(t, "/api/v1/agent/tasks", rec.path)
	assert.Empty(t, rec.query.Get("project"), "no project argument must not produce project=")
}

// T153: group_by/tree are forwarded verbatim; unknown group_by
// VALUES error on the tool side before any HTTP call; tree without
// group_by errors too.
func TestOrendaTools_ListTasksGroupingForwarding(t *testing.T) {
	for _, tc := range []struct {
		name        string
		args        map[string]any
		wantGroupBy string
		wantTree    string
		wantCalled  bool
		wantErrSub  string
	}{
		{"group_by project", map[string]any{"group_by": "project"}, "project", "", true, ""},
		{"group_by + tree", map[string]any{"group_by": "project", "tree": true}, "project", "true", true, ""},
		{"invalid group_by value", map[string]any{"group_by": "status"}, "", "", false, `invalid group_by`},
		{"tree without group_by", map[string]any{"tree": true}, "", "", false, `tree requires group_by`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv, rec := newToolServer(t, nil)
			rawArgs, _ := json.Marshal(tc.args)
			raw := fmt.Sprintf(`{"jsonrpc":"2.0","id":42,"method":"tools/call","params":{"name":"orenda_list_tasks","arguments":%s}}`, rawArgs)
			resp := call(t, srv, raw)
			if tc.wantErrSub != "" {
				errObj, ok := resp["error"].(map[string]any)
				require.True(t, ok, "expected tool error, got %v", resp)
				msg, _ := errObj["message"].(string)
				assert.Contains(t, msg, tc.wantErrSub)
				assert.Empty(t, rec.method, "backend must not be called on invalid value")
				return
			}
			require.Nil(t, resp["error"], "forwarding case must succeed, got %v", resp)
			assert.Equal(t, tc.wantGroupBy, rec.query.Get("group_by"))
			assert.Equal(t, tc.wantTree, rec.query.Get("tree"))
		})
	}
}

// T140: unfamiliar parameter names are a caller bug (mappings
// change, typos) — the tool errors instead of silently dropping
// them, and the backend is never called.
func TestOrendaTools_ListTasksRejectsUnknownParameter(t *testing.T) {
	srv, rec := newToolServer(t, nil)
	raw := `{"jsonrpc":"2.0","id":9,"method":"tools/call","params":{"name":"orenda_list_tasks","arguments":{"bogus":1}}}`
	resp := call(t, srv, raw)
	errObj, ok := resp["error"].(map[string]any)
	require.True(t, ok, "unknown parameter must be a tool error, got %v", resp)
	msg, _ := errObj["message"].(string)
	assert.Contains(t, msg, "unknown parameter")
	assert.Contains(t, msg, "bogus")
	assert.Empty(t, rec.method, "backend must not be called on unknown parameter")
}

func TestOrendaTools_ServerErrorBodyTruncated(t *testing.T) {
	srv, _ := newToolServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write(bytes.Repeat([]byte("x"), 5000))
	})
	resp := call(t, srv, `{"jsonrpc":"2.0","id":8,"method":"tools/call","params":{"name":"orenda_me","arguments":{}}}`)
	errObj, ok := resp["error"].(map[string]any)
	require.True(t, ok, "tool should return an error, got %v", resp)
	msg, _ := errObj["message"].(string)
	assert.Contains(t, msg, "500 Internal Server Error")
	assert.Contains(t, msg, "(truncated)")
	assert.Less(t, len(msg), 700, "message must be capped, got %d chars", len(msg))
}
