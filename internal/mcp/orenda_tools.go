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
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
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
		Name:        "orenda_list_projects",
		Description: "List all projects (id/name) — the programmatic source of project_id for task proposal (orenda_task_propose) and of the project name for branch naming.",
		InputSchema: map[string]any{"type": "object", "properties": map[string]any{}},
		Handler: func(ctx context.Context, _ map[string]any) (any, error) {
			return agentGet(ctx, httpc, cfg, "/api/v1/agent/projects")
		},
	})

	s.Register(Tool{
		Name:        "orenda_list_tasks",
		Description: "List claimable tasks. ?ready=true filters to unblocked, unclaimed, open tasks (the agent's ready-list). Each task carries a human `number` ('T42') alongside its UUID — use the T-prefixed form wherever a task_id is taken. Optional `project` scopes the list to one project (number, P-number or UUID); projects the agent is not granted access to yield no tasks. Optional `group_by: \"project\"` returns {groups:[{project, label, tasks}]}, inbox tasks last (project null, label 'inbox'); optional `tree: true` (requires group_by) nests each group's tasks by parent_task_id with orphaned/cyclic flags.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"ready":    map[string]any{"type": "boolean", "description": "Filter to ready-to-claim only"},
				"limit":    map[string]any{"type": "integer", "description": "Max results (1-100)"},
				"project":  map[string]any{"type": "string", "description": "Scope to one project: number (7), P-number (P7) or UUID; unknown or non-granted projects yield no tasks"},
				"group_by": map[string]any{"type": "string", "enum": []string{"project"}, "description": "Group the result by project (groups[].project / label / tasks; inbox group last)"},
				"tree":     map[string]any{"type": "boolean", "description": "With group_by: nest each group's tasks by parent_task_id (tree[] nodes; orphaned/cyclic roots flagged)"},
			},
		},
		Handler: func(ctx context.Context, params map[string]any) (any, error) {
			// T140: unknown keys are a caller bug — fail loudly instead
			// of silently dropping them. T153: unknown VALUES too.
			allowed := map[string]bool{"ready": true, "limit": true, "project": true, "group_by": true, "tree": true}
			for k := range params {
				if !allowed[k] {
					return nil, fmt.Errorf("unknown parameter %q (allowed: group_by, limit, project, ready, tree)", k)
				}
			}
			groupBy, _ := params["group_by"].(string)
			if groupBy != "" && groupBy != "project" {
				return nil, fmt.Errorf("invalid group_by %q (only \"project\" is supported)", groupBy)
			}
			tree, _ := params["tree"].(bool)
			if tree && groupBy == "" {
				return nil, fmt.Errorf("tree requires group_by=\"project\"")
			}
			q := url.Values{}
			if r, _ := params["ready"].(bool); r {
				q.Set("ready", "true")
			}
			if l, ok := params["limit"].(float64); ok {
				q.Set("limit", fmt.Sprintf("%d", int(l)))
			}
			if p, ok := params["project"].(string); ok && p != "" {
				q.Set("project", p)
			}
			if groupBy != "" {
				q.Set("group_by", groupBy)
			}
			if tree {
				q.Set("tree", "true")
			}
			return agentGet(ctx, httpc, cfg, "/api/v1/agent/tasks?"+q.Encode())
		},
	})

	s.Register(Tool{
		Name:        "orenda_task_propose",
		Description: "Propose a new task. Lands in status=backlog with awaiting=none — the owner triages it on the kanban board (drag to todo / in_progress / done; dismiss = delete). The task is NOT added to the review queue (which is reserved for agent-submitted work). The task becomes claimable via /api/v1/agent/tasks?ready=true only after the owner drags it out of backlog.",
		InputSchema: map[string]any{
			"type":     "object",
			"required": []string{"project_id", "title", "description_md"},
			"properties": map[string]any{
				"project_id":     map[string]any{"type": "string"},
				"title":          map[string]any{"type": "string"},
				"description_md": map[string]any{"type": "string", "description": "Markdown body — the task must be self-sufficient (CONTEXT.md)"},
				"priority":       map[string]any{"type": "string", "description": "low|medium|high|urgent (default medium)"},
				"blocked_by":     map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Blocker task ids"},
				"parent_task_id": map[string]any{"type": "string", "description": "Parent task id (creates a subtask)"},
			},
		},
		Handler: func(ctx context.Context, params map[string]any) (any, error) {
			projectID, _ := params["project_id"].(string)
			title, _ := params["title"].(string)
			desc, _ := params["description_md"].(string)
			if projectID == "" || title == "" || strings.TrimSpace(desc) == "" {
				return nil, fmt.Errorf("project_id, title and description_md are required")
			}
			body := map[string]any{
				"project_id":     projectID,
				"title":          title,
				"description_md": desc,
			}
			if p := stringParam(params, "priority"); p != "" {
				body["priority"] = p
			}
			if p := stringParam(params, "parent_task_id"); p != "" {
				body["parent_task_id"] = p
			}
			if raw, ok := params["blocked_by"].([]any); ok && len(raw) > 0 {
				ids := make([]string, 0, len(raw))
				for _, v := range raw {
					if s, ok := v.(string); ok && s != "" {
						ids = append(ids, s)
					}
				}
				if len(ids) > 0 {
					body["blocked_by"] = ids
				}
			}
			return agentPost(ctx, httpc, cfg, "/api/v1/agent/tasks", body)
		},
	})

	s.Register(Tool{
		Name:        "orenda_task_update",
		Description: "Edit own un-triaged task proposal (status=backlog + awaiting=human) or update agent_notes as the lock holder. Two paths: {agent_notes: ...} only goes through the holder gate; any other field goes through the proposal gate. Returns 403 not_your_proposal / not_lock_holder on permission failure.",
		InputSchema: map[string]any{
			"type":     "object",
			"required": []string{"task_id"},
			"properties": map[string]any{
				"task_id":        map[string]any{"type": "string", "description": "Task UUID or T-prefixed number ('T42')"},
				"title":          map[string]any{"type": "string"},
				"description_md": map[string]any{"type": "string"},
				"priority":       map[string]any{"type": "string", "description": "low|medium|high|urgent"},
				"due_at":         map[string]any{"type": "string", "description": "RFC3339 or null to clear"},
				"parent_task_id": map[string]any{"type": "string"},
				"agent_notes":    map[string]any{"type": "string"},
				"blocked_by": map[string]any{
					"type": "array", "items": map[string]any{"type": "string"},
					"description": "Full replacement blocker list (Task 115). Items are task UUIDs or T-refs ('T42'); [] clears all blockers; omit the field to leave them untouched. Self-blocks and cycles are rejected (422). Adding a blocker auto-flips the task to status=blocked until every blocker is done or removed.",
				},
			},
		},
		Handler: func(ctx context.Context, params map[string]any) (any, error) {
			id, _ := params["task_id"].(string)
			if id == "" {
				return nil, fmt.Errorf("task_id is required")
			}
			body := map[string]any{}
			for _, k := range []string{"title", "description_md", "priority", "due_at", "parent_task_id", "agent_notes"} {
				if v, _ := params[k].(string); v != "" {
					body[k] = v
				}
			}
			// Task 115: blocked_by distinguishes absent (untouched)
			// from [] (clear all) — forward the raw array whenever
			// the field was supplied.
			if raw, ok := params["blocked_by"].([]any); ok {
				ids := make([]string, 0, len(raw))
				for _, v := range raw {
					if s, ok := v.(string); ok && s != "" {
						ids = append(ids, s)
					}
				}
				body["blocked_by"] = ids
			}
			return agentPatch(ctx, httpc, cfg, "/api/v1/agent/tasks/"+url.PathEscape(id), body)
		},
	})

	s.Register(Tool{
		Name:        "orenda_task_retract",
		Description: "Hard-delete own un-triaged task proposal (status=backlog + awaiting=human). Returns 403 not_your_proposal if the task is triaged or created by another agent.",
		InputSchema: map[string]any{
			"type":     "object",
			"required": []string{"task_id"},
			"properties": map[string]any{
				"task_id": map[string]any{"type": "string", "description": "Task UUID or T-prefixed number ('T42')"},
			},
		},
		Handler: func(ctx context.Context, params map[string]any) (any, error) {
			id, _ := params["task_id"].(string)
			if id == "" {
				return nil, fmt.Errorf("task_id is required")
			}
			return agentDelete(ctx, httpc, cfg, "/api/v1/agent/tasks/"+url.PathEscape(id))
		},
	})

	s.Register(Tool{
		Name:        "orenda_claim",
		Description: "Claim a task for the agent. 409 lock_taken if held by another agent; 422 task_blocked with unfinished_blockers list if Phase 15 deps are open.",
		InputSchema: map[string]any{
			"type":     "object",
			"required": []string{"task_id"},
			"properties": map[string]any{
				"task_id": map[string]any{"type": "string", "description": "Task UUID or T-prefixed number ('T42')"},
			},
		},
		Handler: func(ctx context.Context, params map[string]any) (any, error) {
			id, _ := params["task_id"].(string)
			if id == "" {
				return nil, fmt.Errorf("task_id is required")
			}
			return agentPost(ctx, httpc, cfg, "/api/v1/agent/tasks/"+url.PathEscape(id)+"/claim", nil)
		},
	})

	s.Register(Tool{
		Name:        "orenda_release",
		Description: "Release a claim held by this agent.",
		InputSchema: map[string]any{
			"type":     "object",
			"required": []string{"task_id"},
			"properties": map[string]any{
				"task_id": map[string]any{"type": "string", "description": "Task UUID or T-prefixed number ('T42')"},
			},
		},
		Handler: func(ctx context.Context, params map[string]any) (any, error) {
			id, _ := params["task_id"].(string)
			if id == "" {
				return nil, fmt.Errorf("task_id is required")
			}
			return agentPost(ctx, httpc, cfg, "/api/v1/agent/tasks/"+url.PathEscape(id)+"/release", nil)
		},
	})

	s.Register(Tool{
		Name:        "orenda_submit",
		Description: "Mark a task ready for human review (status=review, awaiting=human).",
		InputSchema: map[string]any{
			"type":     "object",
			"required": []string{"task_id"},
			"properties": map[string]any{
				"task_id": map[string]any{"type": "string", "description": "Task UUID or T-prefixed number ('T42')"},
				"note":    map[string]any{"type": "string", "description": "Optional note for the human reviewer"},
			},
		},
		Handler: func(ctx context.Context, params map[string]any) (any, error) {
			id, _ := params["task_id"].(string)
			if id == "" {
				return nil, fmt.Errorf("task_id is required")
			}
			body := map[string]any{}
			if n, _ := params["note"].(string); n != "" {
				body["note"] = n
			}
			return agentPost(ctx, httpc, cfg, "/api/v1/agent/tasks/"+url.PathEscape(id)+"/submit", body)
		},
	})

	s.Register(Tool{
		Name:        "orenda_time",
		Description: "Log manual time on a task (minutes >= 0). Required evidence for orenda_submit when no timer ran; 0 minutes marks the task time-tracked-trivial and passes the submit gate.",
		InputSchema: map[string]any{
			"type":     "object",
			"required": []string{"task_id", "minutes"},
			"properties": map[string]any{
				"task_id": map[string]any{"type": "string", "description": "Task UUID or T-prefixed number ('T42')"},
				"minutes": map[string]any{"type": "number", "description": "Minutes spent (>= 0; 0 = trivial, passes the submit gate)"},
			},
		},
		Handler: func(ctx context.Context, params map[string]any) (any, error) {
			id, _ := params["task_id"].(string)
			if id == "" {
				return nil, fmt.Errorf("task_id is required")
			}
			minutes, _ := params["minutes"].(float64)
			body := map[string]any{"minutes": minutes}
			return agentPost(ctx, httpc, cfg, "/api/v1/agent/tasks/"+url.PathEscape(id)+"/time", body)
		},
	})

	s.Register(Tool{
		Name:        "orenda_context",
		Description: "Full task snapshot: task + comments + activity + children + checklists. Use to resume work after a restart.",
		InputSchema: map[string]any{
			"type":     "object",
			"required": []string{"task_id"},
			"properties": map[string]any{
				"task_id": map[string]any{"type": "string", "description": "Task UUID or T-prefixed number ('T42')"},
			},
		},
		Handler: func(ctx context.Context, params map[string]any) (any, error) {
			id, _ := params["task_id"].(string)
			if id == "" {
				return nil, fmt.Errorf("task_id is required")
			}
			return agentGet(ctx, httpc, cfg, "/api/v1/agent/tasks/"+url.PathEscape(id)+"/context")
		},
	})

	// ------------------------------------------------------------------
	// T96: checklist tools. Thin facades over the agent-namespace
	// checklist routes: list (any agent) + holder-only mutations —
	// the REST layer enforces the 403 not_lock_holder gate, these
	// wrappers just frame the calls.
	// ------------------------------------------------------------------

	s.Register(Tool{
		Name:        "orenda_checklists_list",
		Description: "List a task's checklists with their items (keyed by checklist id). Read any task — use to find the PM's 'Как протестировать' QA checklist before submitting.",
		InputSchema: map[string]any{
			"type":     "object",
			"required": []string{"task_id"},
			"properties": map[string]any{
				"task_id": map[string]any{"type": "string", "description": "Task UUID or T-prefixed number ('T42')"},
			},
		},
		Handler: func(ctx context.Context, params map[string]any) (any, error) {
			id, _ := params["task_id"].(string)
			if id == "" {
				return nil, fmt.Errorf("task_id is required")
			}
			return agentGet(ctx, httpc, cfg, "/api/v1/agent/tasks/"+url.PathEscape(id)+"/checklists")
		},
	})

	s.Register(Tool{
		Name:        "orenda_checklist_add",
		Description: "Create a checklist on a task (lock holder only; 403 not_lock_holder otherwise).",
		InputSchema: map[string]any{
			"type":     "object",
			"required": []string{"task_id", "title"},
			"properties": map[string]any{
				"task_id": map[string]any{"type": "string", "description": "Task UUID or T-prefixed number ('T42')"},
				"title":   map[string]any{"type": "string"},
			},
		},
		Handler: func(ctx context.Context, params map[string]any) (any, error) {
			id, _ := params["task_id"].(string)
			title, _ := params["title"].(string)
			if id == "" || title == "" {
				return nil, fmt.Errorf("task_id and title are required")
			}
			return agentPost(ctx, httpc, cfg,
				"/api/v1/agent/tasks/"+url.PathEscape(id)+"/checklists",
				map[string]any{"title": title})
		},
	})

	s.Register(Tool{
		Name:        "orenda_checklist_item_add",
		Description: "Append an item to a checklist (lock holder only). Get checklist ids from orenda_checklists_list.",
		InputSchema: map[string]any{
			"type":     "object",
			"required": []string{"task_id", "checklist_id", "title"},
			"properties": map[string]any{
				"task_id":      map[string]any{"type": "string", "description": "Task UUID or T-prefixed number ('T42')"},
				"checklist_id": map[string]any{"type": "string"},
				"title":        map[string]any{"type": "string"},
			},
		},
		Handler: func(ctx context.Context, params map[string]any) (any, error) {
			id, _ := params["task_id"].(string)
			listID, _ := params["checklist_id"].(string)
			title, _ := params["title"].(string)
			if id == "" || listID == "" || title == "" {
				return nil, fmt.Errorf("task_id, checklist_id and title are required")
			}
			return agentPost(ctx, httpc, cfg,
				"/api/v1/agent/tasks/"+url.PathEscape(id)+"/checklists/"+url.PathEscape(listID)+"/items",
				map[string]any{"title": title})
		},
	})

	s.Register(Tool{
		Name:        "orenda_checklist_item_update",
		Description: "Update a checklist item: tick done or edit the title (lock holder only). Ticking done tells the owner the step was verified.",
		InputSchema: map[string]any{
			"type":     "object",
			"required": []string{"task_id", "checklist_id", "item_id"},
			"properties": map[string]any{
				"task_id":      map[string]any{"type": "string", "description": "Task UUID or T-prefixed number ('T42')"},
				"checklist_id": map[string]any{"type": "string"},
				"item_id":      map[string]any{"type": "string"},
				"done":         map[string]any{"type": "boolean", "description": "Tick / untick the item"},
				"title":        map[string]any{"type": "string", "description": "Replace the item title"},
			},
		},
		Handler: func(ctx context.Context, params map[string]any) (any, error) {
			id, _ := params["task_id"].(string)
			listID, _ := params["checklist_id"].(string)
			itemID, _ := params["item_id"].(string)
			if id == "" || listID == "" || itemID == "" {
				return nil, fmt.Errorf("task_id, checklist_id and item_id are required")
			}
			body := map[string]any{}
			if done, ok := params["done"].(bool); ok {
				body["done"] = done
			}
			if t, _ := params["title"].(string); t != "" {
				body["title"] = t
			}
			if len(body) == 0 {
				return nil, fmt.Errorf("nothing to update: pass done and/or title")
			}
			return agentPatch(ctx, httpc, cfg,
				"/api/v1/agent/tasks/"+url.PathEscape(id)+"/checklists/"+url.PathEscape(listID)+"/items/"+url.PathEscape(itemID),
				body)
		},
	})

	s.Register(Tool{
		Name:        "orenda_checklist_item_delete",
		Description: "Delete a checklist item (lock holder only).",
		InputSchema: map[string]any{
			"type":     "object",
			"required": []string{"task_id", "checklist_id", "item_id"},
			"properties": map[string]any{
				"task_id":      map[string]any{"type": "string", "description": "Task UUID or T-prefixed number ('T42')"},
				"checklist_id": map[string]any{"type": "string"},
				"item_id":      map[string]any{"type": "string"},
			},
		},
		Handler: func(ctx context.Context, params map[string]any) (any, error) {
			id, _ := params["task_id"].(string)
			listID, _ := params["checklist_id"].(string)
			itemID, _ := params["item_id"].(string)
			if id == "" || listID == "" || itemID == "" {
				return nil, fmt.Errorf("task_id, checklist_id and item_id are required")
			}
			return agentDelete(ctx, httpc, cfg,
				"/api/v1/agent/tasks/"+url.PathEscape(id)+"/checklists/"+url.PathEscape(listID)+"/items/"+url.PathEscape(itemID))
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
		Description: "Fetch a wiki page by slug or W-ref (e.g. 'W15'). Title + markdown content.",
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
				return nil, fmt.Errorf("slug is required")
			}
			return agentGet(ctx, httpc, cfg, "/api/v1/agent/pages/"+url.PathEscape(slug))
		},
	})

	s.Register(Tool{
		Name:        "orenda_pages_save",
		Description: "Create or update a wiki page (upsert by slug). W<digits> slugs are rejected (use the real slug). Markdown content; [[slug]] links are indexed automatically.",
		InputSchema: map[string]any{
			"type":     "object",
			"required": []string{"slug", "title"},
			"properties": map[string]any{
				"slug":       map[string]any{"type": "string", "description": "Page slug (not a W-ref; W<digits> rejected on create)"},
				"title":      map[string]any{"type": "string"},
				"content_md": map[string]any{"type": "string", "description": "Markdown body"},
				"parent_id":  map[string]any{"type": "string", "description": "Parent page id (omit = root)"},
			},
		},
		Handler: func(ctx context.Context, params map[string]any) (any, error) {
			slug, _ := params["slug"].(string)
			title, _ := params["title"].(string)
			if slug == "" || title == "" {
				return nil, fmt.Errorf("slug and title are required")
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
		Description: "Delete a wiki page by slug or W-ref (e.g. 'W15'). Children cascade.",
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
				return nil, fmt.Errorf("slug is required")
			}
			return agentDelete(ctx, httpc, cfg, "/api/v1/agent/pages/"+url.PathEscape(slug))
		},
	})

	s.Register(Tool{
		Name:        "orenda_pages_move",
		Description: "Move a wiki page by slug or W-ref (e.g. 'W15') under a new parent (empty parent_id = root).",
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
				return nil, fmt.Errorf("slug and parent_id are required")
			}
			return agentPatch(ctx, httpc, cfg,
				"/api/v1/agent/pages/"+url.PathEscape(slug)+"/move",
				map[string]any{"parent_id": parent})
		},
	})

	s.Register(Tool{
		Name: "orenda_pages_blocks",
		Description: "Read or replace the block tree of a wiki page (by slug or W-ref, e.g. 'W15'). " +
			"Without `blocks`: returns the BlockView {format, content_md, blocks}. " +
			"With `blocks`: REPLACES the ENTIRE block tree of the page — anything not included in the call is lost; GET first and send back the edited tree. " +
			"Block format: {id: string (required), type: string (required), props: object (optional), content: array (optional inline content), children: array of Block (optional nested)}",
		InputSchema: map[string]any{
			"type":     "object",
			"required": []string{"slug"},
			"properties": map[string]any{
				"slug": map[string]any{"type": "string", "description": "Page slug or W-ref (e.g. 'W15')"},
				"blocks": map[string]any{
					"type":  "array",
					"items": map[string]any{"type": "object"},
					"description": "Full replacement tree ({id, type} required; props, content, children optional). " +
						"Omit to read the current BlockView; present = the whole tree is replaced",
				},
			},
		},
		Handler: func(ctx context.Context, params map[string]any) (any, error) {
			slug, _ := params["slug"].(string)
			if slug == "" {
				return nil, fmt.Errorf("slug is required")
			}
			path := "/api/v1/agent/pages/" + url.PathEscape(slug) + "/blocks"
			if blocks, ok := params["blocks"]; ok {
				// Replace mode: the agent endpoint takes the tree
				// wrapped as {"blocks": [...]} and swaps the whole tree.
				return agentPut(ctx, httpc, cfg, path, map[string]any{"blocks": blocks})
			}
			return agentGet(ctx, httpc, cfg, path)
		},
	})

	s.Register(Tool{
		Name:        "orenda_page_attachment_upload",
		Description: "Upload a file (bytes or base64) as an attachment to a wiki page (by slug or W-ref). Returns the attachment row (id, filename, mime) — reference it as /api/v1/attachments/{id}/download in page content.",
		InputSchema: map[string]any{
			"type":     "object",
			"required": []string{"slug", "filename"},
			"properties": map[string]any{
				"slug":     map[string]any{"type": "string", "description": "Page slug or W-ref (e.g. 'W15')"},
				"filename": map[string]any{"type": "string"},
				"content_b64": map[string]any{
					"type":        "string",
					"description": "Base64-encoded file bytes. Omit with content_utf8 empty to probe the endpoint.",
				},
				"content_utf8": map[string]any{
					"type":        "string",
					"description": "File content as UTF-8 text (used when content_b64 is empty)",
				},
				"mime": map[string]any{"type": "string", "description": "MIME type (default: application/octet-stream)"},
			},
		},
		Handler: func(ctx context.Context, params map[string]any) (any, error) {
			slug, _ := params["slug"].(string)
			filename, _ := params["filename"].(string)
			if slug == "" || filename == "" {
				return nil, fmt.Errorf("slug and filename are required")
			}
			mime := stringParam(params, "mime")
			if mime == "" {
				mime = "application/octet-stream"
			}
			var data []byte
			if b64 := stringParam(params, "content_b64"); b64 != "" {
				raw, err := base64.StdEncoding.DecodeString(b64)
				if err != nil {
					return nil, fmt.Errorf("content_b64 is not valid base64: %w", err)
				}
				data = raw
			} else {
				data = []byte(stringParam(params, "content_utf8"))
			}
			return agentUploadFile(ctx, httpc, cfg,
				"/api/v1/agent/pages/"+url.PathEscape(slug)+"/attachments",
				filename, mime, data)
		},
	})

	s.Register(Tool{
		Name:        "orenda_page_attachments_list",
		Description: "List the files attached to a wiki page (by slug or W-ref). Use it before re-uploading: uploading the same bytes again returns the existing attachment (dedup), and the filename column lets a script skip already-migrated files.",
		InputSchema: map[string]any{
			"type":     "object",
			"required": []string{"slug"},
			"properties": map[string]any{
				"slug": map[string]any{"type": "string", "description": "Page slug or W-ref (e.g. 'W15')"},
			},
		},
		Handler: func(ctx context.Context, params map[string]any) (any, error) {
			slug, _ := params["slug"].(string)
			if slug == "" {
				return nil, fmt.Errorf("slug is required")
			}
			return agentGet(ctx, httpc, cfg,
				"/api/v1/agent/pages/"+url.PathEscape(slug)+"/attachments")
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
				return nil, fmt.Errorf("query is required")
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
				return nil, fmt.Errorf("title and target_date are required")
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
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, cfg.OrendaBaseURL+path, http.NoBody)
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

// agentUploadFile POSTs a multipart/form-data body with a single
// `file` part. The agent bearer header rides along like in the JSON
// helpers; non-2xx maps through readBody's error formatting.
func agentUploadFile(
	ctx context.Context, c *http.Client, cfg ServerConfig,
	path, filename, mime string, data []byte,
) (any, error) {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	hdr := make(textproto.MIMEHeader)
	hdr.Set("Content-Disposition", `form-data; name="file"; filename="`+filename+`"`)
	hdr.Set("Content-Type", mime)
	part, err := mw.CreatePart(hdr)
	if err != nil {
		return nil, err
	}
	if _, err := part.Write(data); err != nil {
		return nil, err
	}
	if err := mw.Close(); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.OrendaBaseURL+path, &buf)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+cfg.AgentToken)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err := c.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return readBody(resp)
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

// maxErrBody caps how much of an error response body is embedded
// in the error string — enough to carry the JSON error object,
// not enough to flood the agent's context with an HTML 500 page.
const maxErrBody = 500

func readBody(resp *http.Response) (any, error) {
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		// Surface the server's own words: status line + body, e.g.
		// "server returned 422 Unprocessable Entity:
		// {"error":"invalid_project"}". The tool name is prefixed by
		// the JSON-RPC dispatcher, not here — readBody has no idea
		// which tool triggered the call.
		body := strings.TrimSpace(string(raw))
		if len(body) > maxErrBody {
			body = body[:maxErrBody] + "…(truncated)"
		}
		return nil, fmt.Errorf("server returned %s: %s",
			resp.Status, body)
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return map[string]any{"status": resp.StatusCode}, nil
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		//nolint:nilerr // deliberate: a non-JSON payload is a valid result (raw text), not a failure.
		return string(raw), nil
	}
	return v, nil
}

// helper for the stdio proxy: pick the agent token from env.
func TokenFromEnv() string {
	return os.Getenv("ORENDA_AGENT_TOKEN")
}
