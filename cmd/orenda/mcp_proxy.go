// Package main — `orenda mcp-proxy` (Phase 25.2):
// stdio↔HTTP bridge for the MCP server.
//
// Some MCP clients (notably older Claude Code, some IDE plugins)
// only support stdio transport. They launch a local process
// and pipe JSON-RPC over its stdin/stdout. The Orenda MCP server
// itself runs over HTTP (Streamable HTTP) — this proxy is the
// bridge: it spawns a process per tool call (or holds a long-lived
// connection, depending on the transport), forwards JSON-RPC,
// and adds a thin "spawn or connect" layer.
//
// Implementation: we proxy to a configured HTTP endpoint
// (`--url http://localhost:2137`) and use the agent token. The
// MCP "streamable HTTP" transport accepts POST requests with the
// JSON-RPC envelope; we keep it simple — request/response, no
// SSE for now (tool discovery is one-shot, tool calls are short).
//
// Wire shape:
//
//	client → proxy:    <JSON-RPC request, newline-terminated>
//	proxy → server:    POST <url>/api/v1/agent/mcp  body=<request>
//	                    Authorization: Bearer <token>
//	server → proxy:    <JSON-RPC response, JSON body>
//	proxy → client:    <JSON-RPC response, newline-terminated>
//
// Phase 25.1 ships a stdio-only server (the mcp package's Serve),
// so the proxy uses the same JSON-RPC encoding on the server side
// too. When a real Streamable-HTTP MCP server lands, this proxy
// only changes the URL.
package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/ramgml/orenda/internal/mcp"
)

// newMCPCmd wires the `orenda mcp-proxy` subcommand. Flags:
//
//	--url    Orenda server URL (env: ORENDA_URL)
//	--token  agent bearer token (env: ORENDA_AGENT_TOKEN)
func newMCPCmd() *cobra.Command {
	var (
		url    string
		token  string
		inHTTP bool
	)
	cmd := &cobra.Command{
		Use:   "mcp-proxy",
		Short: "stdio↔HTTP bridge that exposes Orenda as an MCP server",
		Long: `orenda mcp-proxy is a thin bridge: it speaks JSON-RPC 2.0 over
stdin/stdout on one side and forwards every call to Orenda's
HTTP MCP endpoint on the other.

Use this when your MCP client only supports stdio transport
(local subprocess). Configure --url to your running Orenda server
and --token to an agent API token; the proxy handles tool
discovery, call dispatch, and Bearer auth.

Example MCP client config (Claude Code mcpServers):

  "orenda": {
    "command": "/path/to/orenda",
    "args": ["mcp-proxy", "--url", "http://localhost:2137", "--token", "..."],
  }`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if url == "" {
				url = os.Getenv("ORENDA_URL")
			}
			if token == "" {
				token = os.Getenv("ORENDA_AGENT_TOKEN")
			}
			if url == "" {
				return fmt.Errorf("mcp-proxy: --url (or the ORENDA_URL env var) is required; mcp-proxy does not read the agent config file")
			}
			if token == "" {
				return fmt.Errorf("mcp-proxy: --token (or the ORENDA_AGENT_TOKEN env var) is required; mcp-proxy does not read the agent config file")
			}
			if inHTTP {
				return runHTTPProxy(cmd.Context(), url, token)
			}
			return runStdioProxy(cmd.Context(), url, token)
		},
	}
	cmd.Flags().StringVar(&url, "url", "", "Orenda server URL (env: ORENDA_URL)")
	cmd.Flags().StringVar(&token, "token", "", "agent API token (env: ORENDA_AGENT_TOKEN)")
	cmd.Flags().BoolVar(&inHTTP, "http", false, "run as an HTTP server (Phase 25.2 future: bind to a port)")
	return cmd
}

// runStdioProxy speaks JSON-RPC 2.0 over stdio. We embed an
// in-process MCP server (the same one Phase 25.1 ships as a
// library) and forward every call directly to Orenda's HTTP
// namespace. No long-lived HTTP connection — the proxy is a
// local re-implementation that hits Orenda per call.
//
// Why re-implement? Because the stdio server we have is in our
// own `mcp` package (zero deps), and connecting it to Orenda
// here keeps the binary self-contained. When Phase 25.2 ships a
// real Streamable-HTTP server, this proxy can switch to a single
// HTTP transport (one connection, multiplexed).
func runStdioProxy(ctx context.Context, baseURL, token string) error {
	srv := mcp.NewServer("orenda-mcp", "0.2.0-dev")
	mcp.RegisterOrendaTools(srv, mcp.ServerConfig{
		OrendaBaseURL: baseURL,
		AgentToken:    token,
	})
	return srv.Serve(ctx)
}

// runHTTPProxy is the Streamable-HTTP variant. Phase 25.2 stub:
// we run a tiny HTTP server that accepts POST /messages and
// forwards to Orenda's /api/v1/agent/mcp endpoint. Until
// /api/v1/agent/mcp exists (the Phase 25.1 server), this returns
// a 503. We keep the stub so `orenda mcp-proxy --http` doesn't
// surprise the operator with a 404.
func runHTTPProxy(ctx context.Context, baseURL, token string) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/messages", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "POST only", http.StatusMethodNotAllowed)
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		// Forward to Orenda's MCP endpoint. Phase 25.1 wires the
		// server at /api/v1/agent/mcp; the 404 fallback lets the
		// proxy run before 25.1 lands.
		req, err := http.NewRequestWithContext(r.Context(), http.MethodPost,
			baseURL+"/api/v1/agent/mcp", bytes.NewReader(body))
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		c := &http.Client{Timeout: 60 * time.Second}
		resp, err := c.Do(req)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(resp.StatusCode)
		_, _ = io.Copy(w, resp.Body)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "orenda mcp-proxy — POST /messages\n")
	})

	port := os.Getenv("MCP_PROXY_PORT")
	if port == "" {
		port = "7337"
	}
	addr := ":" + port
	srv := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	fmt.Fprintf(os.Stderr, "orenda mcp-proxy listening on %s (forwarding to %s)\n", addr, baseURL)

	// Bridge SIGINT/SIGTERM.
	go func() {
		<-ctx.Done()
		_ = srv.Shutdown(context.Background())
	}()
	return srv.ListenAndServe()
}

// ensure imports stay (used elsewhere; keep the file standalone).
var (
	_ = bufio.NewReader
	_ = json.NewEncoder
)
