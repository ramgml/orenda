// Package mcp — Phase 25: Orenda-specific MCP tools.
//
// Tools in this file wrap the existing /api/v1/agent/* REST
// endpoints so the MCP server is a thin facade — the CLI and the
// UI use the same HTTP surface, the MCP server adds tool discovery
// and JSON-RPC framing on top.

package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// ServerConfig configures the Orenda-side MCP tools.
type ServerConfig struct {
	// OrendaBaseURL is the http://host:port of the running orenda
	// server. The MCP tools proxy every call here.
	OrendaBaseURL string
	// AgentToken is the bearer token used in the Authorization
	// header. Each tool invocation forwards the agent's identity.
	AgentToken string
	// HTTPTimeout bounds each tool call so a stuck Orenda server
	// can't wedge the agent's tool loop. Default: 30s.
	HTTPTimeout time.Duration
}

// RegisterOrendaTools adds the Orenda workflow tools to s. The
// returned Server is ready to Serve().
func RegisterOrendaTools(s *Server, cfg ServerConfig) {
	if cfg.HTTPTimeout == 0 {
		cfg.HTTPTimeout = 30 * time.Second
	}
	httpc := &http.Client{Timeout: cfg.HTTPTimeout}

	// Each tool wraps a single endpoint. We keep them flat rather
	// than nested ("orenda_claim" vs "orenda.tasks.claim") because
	// the MCP spec renders the dot path as a tree on the client
	// side; flat is fine for ~10 tools.

	s.Register(Tool{
		Name:        "orenda_me",
		Description: "Confirm the agent token works. Returns the agent profile bound to the bearer token.",
		InputSchema: map[string]any{"type": "object", "properties": map[string]any{}},
		Handler: func(ctx context.Context, _ map[string]any) (any, error) {
			return agentGet(ctx, httpc, cfg, "/api/v1/agent/me")
		},
	})

	s.Register(Tool{
		Name:        "orenda_list_tasks",
		Description: "List claimable tasks. ?ready=true filters to unblocked, unclaimed, open tasks (the agent's ready-list).",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"ready": map[string]any{"type": "boolean", "description": "Filter to ready-to-claim only"},
				"limit": map[string]any{"type": "integer", "description": "Max results (1-100)"},
			},
		},
		Handler: func(ctx context.Context, params map[string]any) (any, error) {
			q := url.Values{}
			if r, _ := params["ready"].(bool); r {
				q.Set("ready", "true")
			}
			if l, ok := params["limit"].(float64); ok {
				q.Set("limit", fmt.Sprintf("%d", int(l)))
			}
			return agentGet(ctx, httpc, cfg, "/api/v1/agent/tasks?"+q.Encode())
		},
	})

	s.Register(Tool{
		Name:        "orenda_claim",
		Description: "Claim a task for the agent. 409 lock_taken if held by another agent; 422 task_blocked with unfinished_blockers list if Phase 15 deps are open.",
		InputSchema: map[string]any{
			"type":     "object",
			"required": []string{"task_id"},
			"properties": map[string]any{
				"task_id": map[string]any{"type": "string"},
			},
		},
		Handler: func(ctx context.Context, params map[string]any) (any, error) {
			id, _ := params["task_id"].(string)
			if id == "" {
				return nil, fmt.Errorf("orenda_claim: task_id is required")
			}
			return agentPost(ctx, httpc, cfg, "/api/v1/agent/tasks/"+id+"/claim", nil)
		},
	})

	s.Register(Tool{
		Name:        "orenda_release",
		Description: "Release a claim held by this agent.",
		InputSchema: map[string]any{
			"type":     "object",
			"required": []string{"task_id"},
			"properties": map[string]any{
				"task_id": map[string]any{"type": "string"},
			},
		},
		Handler: func(ctx context.Context, params map[string]any) (any, error) {
			id, _ := params["task_id"].(string)
			if id == "" {
				return nil, fmt.Errorf("orenda_release: task_id is required")
			}
			return agentPost(ctx, httpc, cfg, "/api/v1/agent/tasks/"+id+"/release", nil)
		},
	})

	s.Register(Tool{
		Name:        "orenda_submit",
		Description: "Mark a task ready for human review (status=review, awaiting=human).",
		InputSchema: map[string]any{
			"type":     "object",
			"required": []string{"task_id"},
			"properties": map[string]any{
				"task_id": map[string]any{"type": "string"},
				"note":    map[string]any{"type": "string", "description": "Optional note for the human reviewer"},
			},
		},
		Handler: func(ctx context.Context, params map[string]any) (any, error) {
			id, _ := params["task_id"].(string)
			if id == "" {
				return nil, fmt.Errorf("orenda_submit: task_id is required")
			}
			body := map[string]any{}
			if n, _ := params["note"].(string); n != "" {
				body["note"] = n
			}
			return agentPost(ctx, httpc, cfg, "/api/v1/agent/tasks/"+id+"/submit", body)
		},
	})

	s.Register(Tool{
		Name:        "orenda_context",
		Description: "Full task snapshot: task + comments + activity + children + checklists. Use to resume work after a restart.",
		InputSchema: map[string]any{
			"type":     "object",
			"required": []string{"task_id"},
			"properties": map[string]any{
				"task_id": map[string]any{"type": "string"},
			},
		},
		Handler: func(ctx context.Context, params map[string]any) (any, error) {
			id, _ := params["task_id"].(string)
			if id == "" {
				return nil, fmt.Errorf("orenda_context: task_id is required")
			}
			return agentGet(ctx, httpc, cfg, "/api/v1/agent/tasks/"+id+"/context")
		},
	})

	s.Register(Tool{
		Name:        "orenda_await",
		Description: "Long-poll for the next event (task.created, task.reviewed, mention.created, etc.). Returns the event or empty when the timeout elapses.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"timeout_s": map[string]any{"type": "integer", "description": "Long-poll timeout, 1-60s. Default 30."},
			},
		},
		Handler: func(ctx context.Context, params map[string]any) (any, error) {
			timeout := 30
			if t, ok := params["timeout_s"].(float64); ok {
				timeout = int(t)
				if timeout < 1 {
					timeout = 1
				}
				if timeout > 60 {
					timeout = 60
				}
			}
			body := map[string]any{"timeout_s": timeout}
			// Phase 29.3: post to the agent-namespace alias. The
			// pre-29.3 tool targeted the user-side /api/v1/events/await,
			// which 401s on an opaque agent token (RequireUser accepts
			// cookie/JWT only) — the MCP await never actually worked.
			return agentPost(ctx, httpc, cfg, "/api/v1/agent/events/await", body)
		},
	})

	// ------------------------------------------------------------------
	// Phase 29.3: wiki + search tools. Same flat naming as the task
	// tools; each wraps one agent-namespace endpoint from Phase 29.1.
	// ------------------------------------------------------------------

	s.Register(Tool{
		Name:        "orenda_pages_list",
		Description: "List the wiki page tree (pages + nested children).",
		InputSchema: map[string]any{"type": "object", "properties": map[string]any{}},
		Handler: func(ctx context.Context, _ map[string]any) (any, error) {
			return agentGet(ctx, httpc, cfg, "/api/v1/agent/pages")
		},
	})

	s.Register(Tool{
		Name:        "orenda_pages_get",
		Description: "Fetch a wiki page by slug (title + markdown content).",
		InputSchema: map[string]any{
			"type":     "object",
			"required": []string{"slug"},
			"properties": map[string]any{
				"slug": map[string]any{"type": "string"},
			},
		},
		Handler: func(ctx context.Context, params map[string]any) (any, error) {
			slug, _ := params["slug"].(string)
			if slug == "" {
				return nil, fmt.Errorf("orenda_pages_get: slug is required")
			}
			return agentGet(ctx, httpc, cfg, "/api/v1/agent/pages/"+url.PathEscape(slug))
		},
	})

	s.Register(Tool{
		Name:        "orenda_pages_save",
		Description: "Create or update a wiki page (upsert by slug). Markdown content; [[slug]] links are indexed automatically.",
		InputSchema: map[string]any{
			"type":     "object",
			"required": []string{"slug", "title"},
			"properties": map[string]any{
				"slug":       map[string]any{"type": "string"},
				"title":      map[string]any{"type": "string"},
				"content_md": map[string]any{"type": "string", "description": "Markdown body"},
				"parent_id":  map[string]any{"type": "string", "description": "Parent page id (omit = root)"},
			},
		},
		Handler: func(ctx context.Context, params map[string]any) (any, error) {
			slug, _ := params["slug"].(string)
			title, _ := params["title"].(string)
			if slug == "" || title == "" {
				return nil, fmt.Errorf("orenda_pages_save: slug and title are required")
			}
			body := map[string]any{
				"slug":       slug,
				"title":      title,
				"content_md": stringParam(params, "content_md"),
			}
			if p := stringParam(params, "parent_id"); p != "" {
				body["parent_id"] = p
			}
			return agentPut(ctx, httpc, cfg, "/api/v1/agent/pages/"+url.PathEscape(slug), body)
		},
	})

	s.Register(Tool{
		Name:        "orenda_pages_delete",
		Description: "Delete a wiki page by slug (children cascade).",
		InputSchema: map[string]any{
			"type":     "object",
			"required": []string{"slug"},
			"properties": map[string]any{
				"slug": map[string]any{"type": "string"},
			},
		},
		Handler: func(ctx context.Context, params map[string]any) (any, error) {
			slug, _ := params["slug"].(string)
			if slug == "" {
				return nil, fmt.Errorf("orenda_pages_delete: slug is required")
			}
			return agentDelete(ctx, httpc, cfg, "/api/v1/agent/pages/"+url.PathEscape(slug))
		},
	})

	s.Register(Tool{
		Name:        "orenda_pages_move",
		Description: "Move a wiki page under a new parent (empty parent_id = root).",
		InputSchema: map[string]any{
			"type":     "object",
			"required": []string{"slug", "parent_id"},
			"properties": map[string]any{
				"slug":      map[string]any{"type": "string"},
				"parent_id": map[string]any{"type": "string", "description": "New parent page id; empty string moves to root"},
			},
		},
		Handler: func(ctx context.Context, params map[string]any) (any, error) {
			slug, _ := params["slug"].(string)
			parent, ok := params["parent_id"].(string)
			if slug == "" || !ok {
				return nil, fmt.Errorf("orenda_pages_move: slug and parent_id are required")
			}
			return agentPatch(ctx, httpc, cfg,
				"/api/v1/agent/pages/"+url.PathEscape(slug)+"/move",
				map[string]any{"parent_id": parent})
		},
	})

	s.Register(Tool{
		Name:        "orenda_search",
		Description: "Full-text search across wiki pages, tasks, and comments (FTS5, snippet-highlighted).",
		InputSchema: map[string]any{
			"type":     "object",
			"required": []string{"query"},
			"properties": map[string]any{
				"query": map[string]any{"type": "string"},
				"type":  map[string]any{"type": "string", "description": "Restrict to a hit type (page|task|comment)"},
				"limit": map[string]any{"type": "integer", "description": "Max hits"},
			},
		},
		Handler: func(ctx context.Context, params map[string]any) (any, error) {
			q, _ := params["query"].(string)
			if q == "" {
				return nil, fmt.Errorf("orenda_search: query is required")
			}
			v := url.Values{}
			v.Set("q", q)
			if t := stringParam(params, "type"); t != "" {
				v.Set("type", t)
			}
			if l, ok := params["limit"].(float64); ok {
				v.Set("limit", fmt.Sprintf("%d", int(l)))
			}
			return agentGet(ctx, httpc, cfg, "/api/v1/agent/search?"+v.Encode())
		},
	})

	// ------------------------------------------------------------------
	// Phase 31.8: study-planning surface. The external planner
	// calls `orenda_study_propose` to file a pending proposal; the
	// user accepts/dismisses from the Dashboard tray (Phase 31.6).
	// `orenda_courses_list` is the read side of the planner loop —
	// it returns courses (filtered by status) with progress + pace
	// notes attached, so the planner has everything it needs to
	// propose specific reminders.
	// ------------------------------------------------------------------

	s.Register(Tool{
		Name:        "orenda_courses_list",
		Description: "List courses; with ?status=active the planner's view (progress + pace notes + open lessons).",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"status": map[string]any{"type": "string", "description": "Filter by status (draft|review|active|done|archived). Omit = list all."},
			},
		},
		Handler: func(ctx context.Context, params map[string]any) (any, error) {
			v := url.Values{}
			if s := stringParam(params, "status"); s != "" {
				v.Set("status", s)
			}
			path := "/api/v1/agent/courses"
			if len(v) > 0 {
				path += "?" + v.Encode()
			}
			return agentGet(ctx, httpc, cfg, path)
		},
	})

	s.Register(Tool{
		Name:        "orenda_study_propose",
		Description: "File a pending study proposal that the user can accept/dismiss from the Dashboard tray.",
		InputSchema: map[string]any{
			"type":     "object",
			"required": []string{"title", "target_date"},
			"properties": map[string]any{
				"course_id":   map[string]any{"type": "string", "description": "Optional course link; omit for a free-standing reminder"},
				"title":       map[string]any{"type": "string"},
				"body_md":     map[string]any{"type": "string"},
				"target_date": map[string]any{"type": "string", "description": "YYYY-MM-DD"},
			},
		},
		Handler: func(ctx context.Context, params map[string]any) (any, error) {
			title, _ := params["title"].(string)
			targetDate, _ := params["target_date"].(string)
			if title == "" || targetDate == "" {
				return nil, fmt.Errorf("orenda_study_propose: title and target_date are required")
			}
			body := map[string]any{
				"title":       title,
				"target_date": targetDate,
			}
			if c := stringParam(params, "course_id"); c != "" {
				body["course_id"] = c
			}
			if b := stringParam(params, "body_md"); b != "" {
				body["body_md"] = b
			}
			return agentPost(ctx, httpc, cfg, "/api/v1/agent/study-proposals", body)
		},
	})
}

// stringParam reads an optional string parameter.
func stringParam(params map[string]any, key string) string {
	s, _ := params[key].(string)
	return s
}

// agentGet issues a GET against the agent namespace.
func agentGet(ctx context.Context, c *http.Client, cfg ServerConfig, path string) (any, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, cfg.OrendaBaseURL+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+cfg.AgentToken)
	resp, err := c.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return readBody(resp)
}

func agentPost(
	ctx context.Context, c *http.Client, cfg ServerConfig, path string, body any,
) (any, error) {
	return agentWrite(ctx, c, cfg, http.MethodPost, path, body)
}

// agentPut / agentPatch / agentDelete complete the verb set for the
// Phase 29.3 wiki tools.
func agentPut(ctx context.Context, c *http.Client, cfg ServerConfig, path string, body any) (any, error) {
	return agentWrite(ctx, c, cfg, http.MethodPut, path, body)
}

func agentPatch(ctx context.Context, c *http.Client, cfg ServerConfig, path string, body any) (any, error) {
	return agentWrite(ctx, c, cfg, http.MethodPatch, path, body)
}

func agentDelete(ctx context.Context, c *http.Client, cfg ServerConfig, path string) (any, error) {
	return agentWrite(ctx, c, cfg, http.MethodDelete, path, nil)
}

func agentWrite(
	ctx context.Context, c *http.Client, cfg ServerConfig, method, path string, body any,
) (any, error) {
	var rdr io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		rdr = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, cfg.OrendaBaseURL+path, rdr)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+cfg.AgentToken)
	if rdr != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return readBody(resp)
}

func readBody(resp *http.Response) (any, error) {
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		// Surface a structured error so the agent sees a real reason
		// (HTTP 409 lock_taken, 422 task_blocked) instead of a
		// useless "tool error" line.
		body := map[string]any{
			"status": resp.StatusCode,
			"body":   string(raw),
		}
		// Try to parse as JSON so the body shows structured.
		var asJSON any
		if json.Unmarshal(raw, &asJSON) == nil {
			body["body_parsed"] = asJSON
		}
		return nil, fmt.Errorf("orenda: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return map[string]any{"status": resp.StatusCode}, nil
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		// Not JSON — return raw.
		return string(raw), nil
	}
	return v, nil
}

// helper for the stdio proxy: pick the agent token from env.
func TokenFromEnv() string {
	return os.Getenv("ORENDA_AGENT_TOKEN")
}
