// Package mcp — Phase 25: minimal MCP server (stdio transport).
//
// We deliberately don't pull in the official Go SDK
// (github.com/modelcontextprotocol/go-sdk) for v1. The SDK is in
// flux and its version constraints on go.mod are not yet
// "boring"; a 200-line stdlib implementation gets us the bulk of
// the value (tool discovery + JSON-RPC) without the dependency
// surface. A future follow-up can swap this for the SDK once
// we have a real MCP client to integrate against.
//
// Transport: stdio (newline-delimited JSON-RPC 2.0). This is
// the transport the Model Context Protocol uses when launching a
// local server via `mcp-proxy` or directly. Streamable HTTP is the
// remote variant; the `orenda mcp-proxy` subcommand bridges the
// two — a client without stdio support speaks HTTP to the proxy,
// which in turn spawns this server over stdio.
//
// Wire format:
//
//	{ "jsonrpc": "2.0", "id": <n>, "method": "...", "params": {...} }
//	{ "jsonrpc": "2.0", "id": <n>, "result": {...} | "error": {...} }
//
// Methods we support:
//
//	initialize         — handshake, returns server info + capabilities
//	ping               — health
//	tools/list         — enumerate registered tools
//	tools/call         — invoke a tool
//
// Tools are registered via Register() before Serve. Each tool is
// (name, description, input schema, handler). The schema is a
// minimal JSON-Schema-style object — sufficient for the Orenda
// agent workflow.
package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sync"
)

// Server is a JSON-RPC 2.0 server over stdio. Zero dependencies
// beyond stdlib.
type Server struct {
	name    string
	version string

	mu    sync.RWMutex
	tools map[string]Tool
}

// Tool is one MCP tool declaration. InputSchema is a JSON Schema
// fragment — the protocol only needs the "type", "properties",
// and "required" fields for clients to render a UI.
type Tool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
	Handler     ToolHandler
}

// ToolHandler is the Go function the tool maps to. It receives the
// JSON-decoded params (a map) plus a context for cancellation, and
// returns either a structured result (any JSON-serialisable value)
// or an error which the server surfaces as a JSON-RPC error.
type ToolHandler func(ctx context.Context, params map[string]any) (any, error)

// NewServer constructs an MCP server. Name and version are
// reported back to the client in the initialize response.
func NewServer(name, version string) *Server {
	return &Server{
		name:    name,
		version: version,
		tools:   make(map[string]Tool),
	}
}

// Register adds a tool. Names must be unique; duplicates overwrite.
func (s *Server) Register(t Tool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tools[t.Name] = t
}

// Serve runs the JSON-RPC loop on stdin/stdout until EOF or error.
// Cancelling ctx exits the loop.
func (s *Server) Serve(ctx context.Context) error {
	in := bufio.NewReader(os.Stdin)
	out := os.Stdout
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		line, err := in.ReadBytes('\n')
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
		resp := s.handle(ctx, line)
		if resp == nil {
			continue // notification (no response)
		}
		if err := writeMessage(out, resp); err != nil {
			return err
		}
	}
}

// jsonRPCRequest is the wire shape of an inbound message.
type jsonRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// jsonRPCResponse is the wire shape of an outbound message.
type jsonRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *jsonRPCError   `json:"error,omitempty"`
}

// jsonRPCError carries the standard fields. We always populate
// `Code` with -32601 (method not found) or -32603 (internal).
type jsonRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

// handle dispatches a single request. Notifications (no id) return
// nil and the caller skips the write.
func (s *Server) handle(ctx context.Context, raw []byte) *jsonRPCResponse {
	var req jsonRPCRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return errorResponse(req.ID, -32700, "parse error", err.Error())
	}
	if req.JSONRPC != "2.0" {
		return errorResponse(req.ID, -32600, "invalid jsonrpc version", req.JSONRPC)
	}
	// Notifications have no id — return nil so the caller skips the
	// write. The MCP spec is JSON-RPC 2.0 with this rule.
	if len(req.ID) == 0 || string(req.ID) == "null" {
		switch req.Method {
		case "ping":
			// Notified-only ping: still parse, but no response.
			return nil
		case "tools/call":
			// Tools may not be called as notifications.
			return nil
		}
		// Unknown notification: ignore.
		return nil
	}
	switch req.Method {
	case "initialize":
		return s.handleInitialize(req.ID)
	case "ping":
		return okResponse(req.ID, map[string]any{})
	case "tools/list":
		return s.handleToolsList(req.ID)
	case "tools/call":
		return s.handleToolsCall(ctx, req.ID, req.Params)
	default:
		return errorResponse(req.ID, -32601, "method not found", req.Method)
	}
}

func (s *Server) handleInitialize(id json.RawMessage) *jsonRPCResponse {
	return okResponse(id, map[string]any{
		"protocolVersion": "2024-11-05",
		"serverInfo": map[string]any{
			"name":    s.name,
			"version": s.version,
		},
		"capabilities": map[string]any{
			"tools": map[string]any{},
		},
	})
}

func (s *Server) handleToolsList(id json.RawMessage) *jsonRPCResponse {
	s.mu.RLock()
	defer s.mu.RUnlock()
	tools := make([]map[string]any, 0, len(s.tools))
	for _, t := range s.tools {
		// Strip the Handler — it's not part of the wire shape.
		// Returning a map[string]any directly avoids the
		// unserializable ToolHandler type field.
		tools = append(tools, map[string]any{
			"name":        t.Name,
			"description": t.Description,
			"inputSchema": t.InputSchema,
		})
	}
	return okResponse(id, map[string]any{"tools": tools})
}

func (s *Server) handleToolsCall(
	ctx context.Context,
	id json.RawMessage,
	rawParams json.RawMessage,
) *jsonRPCResponse {
	var params struct {
		Name      string         `json:"name"`
		Arguments map[string]any `json:"arguments"`
	}
	if err := json.Unmarshal(rawParams, &params); err != nil {
		return errorResponse(id, -32602, "invalid params", err.Error())
	}
	s.mu.RLock()
	t, ok := s.tools[params.Name]
	s.mu.RUnlock()
	if !ok {
		return errorResponse(id, -32601, "tool not found", params.Name)
	}
	if t.Handler == nil {
		return errorResponse(id, -32603, "tool has no handler", params.Name)
	}
	result, err := t.Handler(ctx, params.Arguments)
	if err != nil {
		// Prefix the tool name into the JSON-RPC message itself, not
		// just the `data` field: many clients (and agents) render
		// only `message`, so a bare "tool error" hides the actual
		// reason. The full detail stays in `data` as well.
		detail := fmt.Sprintf("%s: %v", t.Name, err)
		return errorResponse(id, -32603, detail, detail)
	}
	return okResponse(id, map[string]any{
		"content": []map[string]any{
			{"type": "text", "text": stringifyResult(result)},
		},
	})
}

// stringifyResult JSON-encodes a tool result for the MCP "text"
// content block. If the result is already a string, pass through.
func stringifyResult(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	if v == nil {
		return ""
	}
	raw, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Sprintf("%v", v)
	}
	return string(raw)
}

func okResponse(id json.RawMessage, result any) *jsonRPCResponse {
	return &jsonRPCResponse{JSONRPC: "2.0", ID: id, Result: result}
}

func errorResponse(id json.RawMessage, code int, msg string, data any) *jsonRPCResponse {
	return &jsonRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error: &jsonRPCError{
			Code:    code,
			Message: msg,
			Data:    data,
		},
	}
}

func writeMessage(w io.Writer, msg *jsonRPCResponse) error {
	raw, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	if _, err := w.Write(append(raw, '\n')); err != nil {
		return err
	}
	return nil
}
