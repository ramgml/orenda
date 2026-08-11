// Phase 25: MCP server smoke test.
//
// We exercise the per-message handler directly (handle(ctx, raw))
// rather than spinning up the full Serve() loop. The handler is
// pure: take bytes, return a response (or nil for notifications).
// The Serve() loop is a thin line-reader on top.
package mcp

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestServer() *Server {
	srv := NewServer("orenda-test", "0.0.0")
	return srv
}

func call(t *testing.T, srv *Server, raw string) map[string]any {
	t.Helper()
	resp := srv.handle(context.Background(), []byte(raw))
	require.NotNil(t, resp, "expected a response for %s", raw)
	// Round-trip through JSON to coerce types the same way a real
	// client would.
	js, err := json.Marshal(resp)
	require.NoError(t, err)
	var got map[string]any
	require.NoError(t, json.Unmarshal(js, &got))
	return got
}

func TestServer_Initialize(t *testing.T) {
	srv := newTestServer()
	resp := call(t, srv, `{"jsonrpc":"2.0","id":1,"method":"initialize"}`)
	assert.Equal(t, "2.0", resp["jsonrpc"])
	assert.Equal(t, float64(1), resp["id"])

	result, ok := resp["result"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "2024-11-05", result["protocolVersion"])

	info, ok := result["serverInfo"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "orenda-test", info["name"])
}

func TestServer_Ping(t *testing.T) {
	srv := newTestServer()
	resp := call(t, srv, `{"jsonrpc":"2.0","id":2,"method":"ping"}`)
	assert.NotNil(t, resp["result"])
}

func TestServer_ToolsList(t *testing.T) {
	srv := newTestServer()
	srv.Register(Tool{
		Name:        "echo",
		Description: "Echoes back its input",
		InputSchema: map[string]any{"type": "object"},
		Handler: func(_ context.Context, _ map[string]any) (any, error) {
			return "ok", nil
		},
	})
	resp := call(t, srv, `{"jsonrpc":"2.0","id":3,"method":"tools/list"}`)
	result := resp["result"].(map[string]any)
	tools := result["tools"].([]any)
	require.Len(t, tools, 1)
	t0 := tools[0].(map[string]any)
	assert.Equal(t, "echo", t0["name"])
}

func TestServer_ToolsCall(t *testing.T) {
	srv := newTestServer()
	srv.Register(Tool{
		Name:        "echo",
		Description: "Echoes back its input",
		InputSchema: map[string]any{"type": "object"},
		Handler: func(_ context.Context, params map[string]any) (any, error) {
			return params, nil
		},
	})
	resp := call(t, srv, `{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"echo","arguments":{"x":1}}}`)
	result := resp["result"].(map[string]any)
	content := result["content"].([]any)
	require.Len(t, content, 1)
	c0 := content[0].(map[string]any)
	assert.Equal(t, "text", c0["type"])
	assert.Contains(t, c0["text"], `"x": 1`)
}

func TestServer_MethodNotFound(t *testing.T) {
	srv := newTestServer()
	resp := call(t, srv, `{"jsonrpc":"2.0","id":5,"method":"nope"}`)
	errObj, ok := resp["error"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, float64(-32601), errObj["code"])
}

func TestServer_NotificationNoResponse(t *testing.T) {
	srv := newTestServer()
	resp := srv.handle(context.Background(),
		[]byte(`{"jsonrpc":"2.0","method":"ping"}`))
	assert.Nil(t, resp, "notifications should not produce a response")
}

func TestServer_InvalidJSON(t *testing.T) {
	srv := newTestServer()
	resp := srv.handle(context.Background(), []byte(`{garbage`))
	require.NotNil(t, resp)
	js, err := json.Marshal(resp)
	require.NoError(t, err)
	var got map[string]any
	require.NoError(t, json.Unmarshal(js, &got))
	errObj, ok := got["error"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, float64(-32700), errObj["code"]) // parse error
}

func TestServer_ToolNotFound(t *testing.T) {
	srv := newTestServer()
	resp := call(t, srv, `{"jsonrpc":"2.0","id":6,"method":"tools/call","params":{"name":"nope"}}`)
	errObj, ok := resp["error"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, float64(-32601), errObj["code"])
}

func TestServer_StringifyResult(t *testing.T) {
	srv := newTestServer()
	srv.Register(Tool{
		Name: "x", Description: "x",
		InputSchema: map[string]any{"type": "object"},
		Handler: func(_ context.Context, _ map[string]any) (any, error) {
			return "hello", nil
		},
	})
	resp := call(t, srv, `{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"x"}}`)
	c0 := resp["result"].(map[string]any)["content"].([]any)[0].(map[string]any)
	assert.Equal(t, "hello", c0["text"])
}
