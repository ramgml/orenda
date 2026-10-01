package mcp

// master-agent-role plan (step 7): the project-admin tools proxy the
// USER namespace with the caller's bearer token. Pinned here: name →
// method+path mapping, ref passthrough (server resolves 7 / P7 / UUID),
// grant-list read vs replace, and the 401 passthrough a non-master
// agent receives.

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOrendaTools_ListIncludesProjectAdminTools(t *testing.T) {
	srv, _ := newToolServer(t, nil)
	resp := call(t, srv, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	tools := resp["result"].(map[string]any)["tools"].([]any)
	names := map[string]string{}
	for _, tool := range tools {
		tm := tool.(map[string]any)
		names[tm["name"].(string)], _ = tm["description"].(string)
	}
	for _, want := range []string{
		"orenda_project_create", "orenda_project_update",
		"orenda_project_delete", "orenda_project_agents",
	} {
		desc, ok := names[want]
		require.True(t, ok, "tools/list must include %s", want)
		assert.Contains(t, desc, "master agents only", "%s must be marked master-only", want)
	}
}

func TestOrendaTools_ProjectCreateSendsPOST(t *testing.T) {
	srv, rec := newToolServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"p-9","name":"MCP","agent_token":{"plain_token":"one-time"}}`))
	})
	result := callTool(t, srv, "orenda_project_create", map[string]any{
		"name": "MCP", "color": "#ff0000", "description": "d",
	})
	assert.Equal(t, http.MethodPost, rec.method)
	assert.Equal(t, "/api/v1/projects", rec.path)
	assert.Equal(t, "Bearer tok-abc", rec.authHdr)
	assert.Equal(t, "MCP", rec.body["name"])
	assert.Equal(t, "#ff0000", rec.body["color"])

	content := result["content"].([]any)
	text := content[0].(map[string]any)["text"].(string)
	assert.Contains(t, text, "one-time", "the one-time agent_token passes through verbatim")
}

func TestOrendaTools_ProjectCreateRequiresName(t *testing.T) {
	srv, _ := newToolServer(t, nil)
	raw := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"orenda_project_create","arguments":{}}}`
	resp := call(t, srv, raw)
	errObj, ok := resp["error"].(map[string]any)
	require.True(t, ok, "missing name must fail, got %v", resp)
	assert.Contains(t, errObj["message"], "name is required")
}

func TestOrendaTools_ProjectUpdateSendsPATCHWithRef(t *testing.T) {
	srv, rec := newToolServer(t, nil)
	callTool(t, srv, "orenda_project_update", map[string]any{
		"project": "7", "name": "Renamed", "archived": true,
	})
	assert.Equal(t, http.MethodPatch, rec.method)
	assert.Equal(t, "/api/v1/projects/7", rec.path)
	assert.Equal(t, "Renamed", rec.body["name"])
	assert.Equal(t, true, rec.body["archived"])
	_, hasColor := rec.body["color"]
	assert.False(t, hasColor, "omitted fields stay out of the PATCH body")
}

func TestOrendaTools_ProjectDeleteSendsDELETE(t *testing.T) {
	srv, rec := newToolServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	callTool(t, srv, "orenda_project_delete", map[string]any{"project": "P7"})
	assert.Equal(t, http.MethodDelete, rec.method)
	assert.Equal(t, "/api/v1/projects/P7", rec.path)
}

func TestOrendaTools_ProjectAgentsReadAndReplace(t *testing.T) {
	// Read: no agent_ids → GET.
	srv, rec := newToolServer(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"agent_ids":["a-1"]}`))
	})
	callTool(t, srv, "orenda_project_agents", map[string]any{"project": "7"})
	assert.Equal(t, http.MethodGet, rec.method)
	assert.Equal(t, "/api/v1/projects/7/agents", rec.path)

	// Replace: agent_ids → PUT with the array.
	srv2, rec2 := newToolServer(t, nil)
	callTool(t, srv2, "orenda_project_agents", map[string]any{
		"project": "7", "agent_ids": []any{"a-1", "a-2"},
	})
	assert.Equal(t, http.MethodPut, rec2.method)
	assert.Equal(t, "/api/v1/projects/7/agents", rec2.path)
	ids, _ := rec2.body["agent_ids"].([]any)
	require.Len(t, ids, 2)
	assert.Equal(t, "a-1", ids[0])
}

func TestOrendaTools_ProjectAdminSurfaces401(t *testing.T) {
	// A project agent's token hits the user namespace → the server's
	// 401 passes through as the tool error, no masking.
	srv, _ := newToolServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`unauthorized`))
	})
	raw := fmt.Sprintf(
		`{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"orenda_project_create","arguments":{"name":"x"}}}`,
	)
	resp := call(t, srv, raw)
	errObj, ok := resp["error"].(map[string]any)
	require.True(t, ok, "401 must surface as tool error, got %v", resp)
	assert.Contains(t, errObj["message"], "401")
}
