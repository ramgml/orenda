// T96: checklist MCP tools — wire-shape tests against a recording
// backend. Each tool must hit its agent-namespace route with the
// right method/path/body and forward the bearer token; the REST
// layer owns the gating, so here we only pin the framing.
package mcp

import (
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOrendaTools_ChecklistsListSendsGET(t *testing.T) {
	srv, rec := newToolServer(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"checklists":[{"id":"cl-1","title":"Как протестировать"}],"checklist_items":{}}`))
	})
	result := callTool(t, srv, "orenda_checklists_list", map[string]any{"task_id": "T96"})
	assert.Equal(t, http.MethodGet, rec.method)
	assert.Equal(t, "/api/v1/agent/tasks/T96/checklists", rec.path)
	assert.Equal(t, "Bearer tok-abc", rec.authHdr)
	text := result["content"].([]any)[0].(map[string]any)["text"].(string)
	assert.Contains(t, text, "Как протестировать")
}

func TestOrendaTools_ChecklistAddSendsPOST(t *testing.T) {
	srv, rec := newToolServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"cl-9","title":"QA"}`))
	})
	callTool(t, srv, "orenda_checklist_add", map[string]any{"task_id": "t-1", "title": "QA"})
	assert.Equal(t, http.MethodPost, rec.method)
	assert.Equal(t, "/api/v1/agent/tasks/t-1/checklists", rec.path)
	assert.Equal(t, "QA", rec.body["title"])
}

func TestOrendaTools_ChecklistItemAddSendsPOST(t *testing.T) {
	srv, rec := newToolServer(t, nil)
	callTool(t, srv, "orenda_checklist_item_add",
		map[string]any{"task_id": "t-1", "checklist_id": "cl 1", "title": "make test"})
	assert.Equal(t, http.MethodPost, rec.method)
	assert.Equal(t, "/api/v1/agent/tasks/t-1/checklists/cl 1/items", rec.path)
	assert.Equal(t, "make test", rec.body["title"])
}

func TestOrendaTools_ChecklistItemUpdateSendsPatchBody(t *testing.T) {
	srv, rec := newToolServer(t, nil)
	callTool(t, srv, "orenda_checklist_item_update", map[string]any{
		"task_id": "t-1", "checklist_id": "cl-1", "item_id": "it-2", "done": true,
	})
	assert.Equal(t, http.MethodPatch, rec.method)
	assert.Equal(t, "/api/v1/agent/tasks/t-1/checklists/cl-1/items/it-2", rec.path)
	assert.Equal(t, true, rec.body["done"])

	// Title-only update on a fresh recorder (rec.body is shared
	// across calls on the same backend).
	srv2, rec2 := newToolServer(t, nil)
	callTool(t, srv2, "orenda_checklist_item_update", map[string]any{
		"task_id": "t-1", "checklist_id": "cl-1", "item_id": "it-2", "title": "renamed",
	})
	assert.Equal(t, "renamed", rec2.body["title"])
	_, hasDone := rec2.body["done"]
	assert.False(t, hasDone, "done must stay absent when not passed")
}

func TestOrendaTools_ChecklistItemUpdateRequiresPayload(t *testing.T) {
	srv, _ := newToolServer(t, nil)
	err := callToolErr(t, srv, "orenda_checklist_item_update", map[string]any{
		"task_id": "t-1", "checklist_id": "cl-1", "item_id": "it-2",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nothing to update")
}

func TestOrendaTools_ChecklistItemDeleteSendsDELETE(t *testing.T) {
	srv, rec := newToolServer(t, nil)
	callTool(t, srv, "orenda_checklist_item_delete", map[string]any{
		"task_id": "t-1", "checklist_id": "cl-1", "item_id": "it-2",
	})
	assert.Equal(t, http.MethodDelete, rec.method)
	assert.Equal(t, "/api/v1/agent/tasks/t-1/checklists/cl-1/items/it-2", rec.path)
}

func TestOrendaTools_ChecklistToolsValidateRequiredArgs(t *testing.T) {
	srv, rec := newToolServer(t, nil)

	err := callToolErr(t, srv, "orenda_checklists_list", map[string]any{})
	require.ErrorContains(t, err, "task_id is required")

	err = callToolErr(t, srv, "orenda_checklist_add", map[string]any{"task_id": "t"})
	require.ErrorContains(t, err, "title")

	err = callToolErr(t, srv, "orenda_checklist_item_add", map[string]any{"task_id": "t", "checklist_id": "c"})
	require.ErrorContains(t, err, "title")

	err = callToolErr(t, srv, "orenda_checklist_item_delete", map[string]any{"task_id": "t"})
	require.ErrorContains(t, err, "item_id")

	assert.Equal(t, "", rec.path, "validation must fail before any HTTP call")
}

// callToolErr invokes a tool expecting the handler to fail — the
// server surfaces handler errors as a JSON-RPC error response whose
// message is prefixed with the tool name.
func callToolErr(t *testing.T, srv *Server, name string, args map[string]any) error {
	t.Helper()
	rawArgs, err := json.Marshal(args)
	require.NoError(t, err)
	raw := `{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"` + name + `","arguments":` + string(rawArgs) + `}}`
	resp := call(t, srv, raw)
	if e, ok := resp["error"].(map[string]any); ok {
		msg, _ := e["message"].(string)
		return errors.New(msg)
	}
	return nil
}
