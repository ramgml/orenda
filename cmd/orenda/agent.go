// Package main — `orenda agent` CLI surface (Phase 25).
//
// The agent CLI is the second integration surface for external
// agents (alongside the REST API and the long-poll endpoint). It
// mirrors the workflow shape, not the URL space: a subcommand
// captures one action, exit codes let shell loops do the right
// thing, and `-json` everywhere makes piping into other tools
// cheap.
//
// Configuration precedence (highest first):
//
//	flag  >  env (ORENDA_URL / ORENDA_AGENT_TOKEN)  >  config file
//
// Exit codes:
//
//	0  ok
//	1  generic failure (network, bad response)
//	2  "no work" — set on `await` timeout and `next` empty inbox;
//	   lets `while orenda agent next; do ...; done` work.
//
// We deliberately keep the CLI transport-agnostic: every command
// is a thin shim over the same HTTP the agent-namespace handlers
// expose. The skill document (docs/skills/orenda/SKILL.md) tells
// the agent HOW to drive these commands.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

// agentCtx is the resolved connection target — base URL + token.
type agentCtx struct {
	BaseURL string
	Token   string
}

// resolveAgentCtx reads the CLI flags, then env, then a config file
// in ~/.config/orenda/agent.yaml. Missing token is a soft error
// only for the no-auth subcommands (`help`).
func resolveAgentCtx(cmd *cobra.Command) (*agentCtx, error) {
	baseURL, _ := cmd.Flags().GetString("url")
	token, _ := cmd.Flags().GetString("token")
	if baseURL == "" {
		baseURL = os.Getenv("ORENDA_URL")
	}
	if token == "" {
		token = os.Getenv("ORENDA_AGENT_TOKEN")
	}
	if baseURL == "" || token == "" {
		// Fall back to the config file whenever either value is
		// still missing — flags and env win per-field over the file.
		path, err := agentConfigPath()
		if err == nil {
			cfg, err := loadAgentConfig(path)
			if err == nil {
				if baseURL == "" {
					baseURL = cfg.URL
				}
				if token == "" {
					token = cfg.Token
				}
			}
		}
	}
	if baseURL == "" || token == "" {
		// Mention the config file path in the error so a fresh
		// machine knows where to put the credentials instead of
		// hunting for the token across the filesystem.
		cfgHint, err := agentConfigPath()
		if err != nil {
			cfgHint = "~/.config/orenda/agent.yaml"
		}
		if baseURL == "" {
			return nil, fmt.Errorf("orenda agent: --url (or ORENDA_URL, or url: in %s) is required", cfgHint)
		}
		return nil, fmt.Errorf("orenda agent: --token (or ORENDA_AGENT_TOKEN, or token: in %s) is required", cfgHint)
	}
	return &agentCtx{BaseURL: baseURL, Token: token}, nil
}

// agentConfig is the YAML file shape — `url` and `token` only.
type agentConfig struct {
	URL   string `yaml:"url"`
	Token string `yaml:"token"`
}

func agentConfigPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "orenda", "agent.yaml"), nil
}

func loadAgentConfig(path string) (*agentConfig, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var c agentConfig
	if err := yaml.Unmarshal(raw, &c); err != nil {
		return nil, err
	}
	return &c, nil
}

// agentGet issues a GET against the agent namespace and returns the
// raw JSON body. Caller decodes.
func (a *agentCtx) agentGet(ctx context.Context, path string) (body []byte, status int, err error) {
	return a.do(ctx, http.MethodGet, path, nil)
}

func (a *agentCtx) agentPost(ctx context.Context, path string, body any) (respBody []byte, status int, err error) {
	var rdr io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return nil, 0, err
		}
		rdr = bytes.NewReader(raw)
	}
	return a.doRaw(ctx, http.MethodPost, path, rdr, "application/json")
}

func (a *agentCtx) do(ctx context.Context, method, path string, body io.Reader) (respBody []byte, status int, err error) {
	return a.doRaw(ctx, method, path, body, "")
}

func (a *agentCtx) doRaw(ctx context.Context, method, path string, body io.Reader, contentType string) (respBody []byte, status int, err error) {
	u, err := url.Parse(a.BaseURL)
	if err != nil {
		return nil, 0, fmt.Errorf("bad base url: %w", err)
	}
	// path may carry a query string ("/api/v1/agent/search?q=…").
	// Assigning it to u.Path verbatim would percent-encode the '?'
	// into the path — which is exactly how the pre-29.2 `next`
	// command silently broke its ?ready=true filter. Split first.
	rel, err := url.Parse(path)
	if err != nil {
		return nil, 0, fmt.Errorf("bad request path: %w", err)
	}
	u.Path = strings.TrimRight(u.Path, "/") + rel.Path
	u.RawQuery = rel.RawQuery
	req, err := http.NewRequestWithContext(ctx, method, u.String(), body)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Authorization", "Bearer "+a.Token)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return raw, resp.StatusCode, nil
}

// printJSON pretty-prints v. With `-json` it stays compact; without
// it we'd pretty-print, but the CLI flag is honoured by the caller.
func printJSON(cmd *cobra.Command, v any) error {
	jsonFlag, _ := cmd.Flags().GetBool("json")
	var (
		raw []byte
		err error
	)
	if jsonFlag {
		raw, err = json.Marshal(v)
	} else {
		raw, err = json.MarshalIndent(v, "", "  ")
	}
	if err != nil {
		return err
	}
	_, _ = cmd.OutOrStdout().Write(raw)
	_, _ = cmd.OutOrStdout().Write([]byte("\n"))
	return nil
}

// printTaskRefHeader prints a human "#N  title  (uuid)" line so the
// operator sees the task's human number next to its id. Suppressed
// under -json (scripts read the same fields from the payload) and
// when the server predates task numbers (number <= 0).
func printTaskRefHeader(cmd *cobra.Command, number int, title, id string) {
	jsonFlag, _ := cmd.Flags().GetBool("json")
	if jsonFlag || number <= 0 {
		return
	}
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "#%d  %s  (%s)\n", number, title, id)
}

// agentPatch issues a PATCH against the agent namespace and returns the
// raw JSON body. Caller decodes. Phase 33.2: same shape as agentPost
// — body is marshalled to JSON here so the caller can pass a map.
func (a *agentCtx) agentPatch(ctx context.Context, path string, payload any) (body []byte, status int, err error) {
	var rdr io.Reader
	if payload != nil {
		raw, err := json.Marshal(payload)
		if err != nil {
			return nil, 0, err
		}
		rdr = bytes.NewReader(raw)
	}
	return a.doRaw(ctx, http.MethodPatch, path, rdr, "application/json")
}

// agentDelete issues a DELETE against the agent namespace and returns
// the raw body. Caller decodes. Phase 33.2: retract uses this.
func (a *agentCtx) agentDelete(ctx context.Context, path string) (body []byte, status int, err error) {
	return a.doRaw(ctx, http.MethodDelete, path, nil, "")
}

// newAgentCmd wires the `orenda agent` subcommand tree.
func newAgentCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "agent",
		Short: "External-agent CLI: claim, submit, and inspect tasks",
		Long: `orenda agent is the agent-side CLI for the Orenda delegation loop.

Workflow shape:
  orenda agent me                     # confirm the token works
  orenda agent next                    # await + claim the next task
  orenda agent propose --project <id> --title ... --description-file task.md
                                       # propose a NEW task (human triages it)
  orenda agent context <task-id>       # pull the full snapshot
  orenda agent checklist-add <task-id> <title>  # QA checklists (T96)
  orenda agent submit <task-id>       # mark ready for human review
  orenda agent release <task-id>      # drop a claim
  orenda agent await                   # long-poll for the next event

<task-id> accepts the UUID or the human number: "42" or "#42"
(tasks carry a sequential number alongside the UUID — it is what
branch names, commit messages and PR titles reference).

Configure via flags, env (ORENDA_URL, ORENDA_AGENT_TOKEN), or
~/.config/orenda/agent.yaml.`,
	}

	// Persistent flags applied to every subcommand.
	pflags := cmd.PersistentFlags()
	pflags.String("url", "", "Orenda server URL (overrides ORENDA_URL)")
	pflags.String("token", "", "agent API token (overrides ORENDA_AGENT_TOKEN)")
	pflags.Bool("json", false, "emit compact JSON instead of pretty")

	cmd.AddCommand(newAgentMeCmd())
	cmd.AddCommand(newAgentNextCmd())
	cmd.AddCommand(newAgentProposeCmd())
	cmd.AddCommand(newAgentContextCmd())
	cmd.AddCommand(newAgentClaimCmd())
	cmd.AddCommand(newAgentReleaseCmd())
	cmd.AddCommand(newAgentSubmitCmd())
	cmd.AddCommand(newAgentTimeCmd())
	cmd.AddCommand(newAgentCommentCmd())
	cmd.AddCommand(newAgentChecklistsCmd())
	cmd.AddCommand(newAgentChecklistAddCmd())
	cmd.AddCommand(newAgentChecklistItemAddCmd())
	cmd.AddCommand(newAgentChecklistItemUpdateCmd())
	cmd.AddCommand(newAgentChecklistItemDeleteCmd())
	cmd.AddCommand(newAgentUpdateCmd())
	cmd.AddCommand(newAgentRetractCmd())
	cmd.AddCommand(newAgentProjectsCmd())
	cmd.AddCommand(newAgentAwaitCmd())
	cmd.AddCommand(newAgentPagesCmd())
	cmd.AddCommand(newAgentSearchCmd())
	cmd.AddCommand(newAgentCoursesCmd())
	cmd.AddCommand(newAgentStudyProposeCmd())
	cmd.AddCommand(newAgentPRWatchCmd())
	return cmd
}

func newAgentMeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "me",
		Short: "Print the agent profile bound to the configured token",
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx, err := resolveAgentCtx(cmd)
			if err != nil {
				return err
			}
			raw, code, err := ctx.agentGet(cmd.Context(), "/api/v1/agent/me")
			if err != nil {
				return err
			}
			if code != http.StatusOK {
				return fmt.Errorf("agent me: HTTP %d: %s", code, raw)
			}
			var v any
			if err := json.Unmarshal(raw, &v); err != nil {
				return err
			}
			return printJSON(cmd, v)
		},
	}
}

// newAgentProjectsCmd wires `orenda agent projects list`.
//
// The list is the agent's programmatic source of project_id —
// required by `orenda agent propose` / `orenda_task_propose` — plus
// the project name for branch naming. Single-owner store, so the
// list is the full project table.
func newAgentProjectsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "projects",
		Short: "Inspect projects (list)",
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "Print all projects (id/name — source of project_id for propose)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx, err := resolveAgentCtx(cmd)
			if err != nil {
				return err
			}
			raw, code, err := ctx.agentGet(cmd.Context(), "/api/v1/agent/projects")
			if err != nil {
				return err
			}
			if code != http.StatusOK {
				return fmt.Errorf("agent projects: HTTP %d: %s", code, raw)
			}
			var v any
			if err := json.Unmarshal(raw, &v); err != nil {
				return err
			}
			return printJSON(cmd, v)
		},
	})
	return cmd
}

func newAgentNextCmd() *cobra.Command {
	var (
		limit     int
		awaitSecs int
	)
	cmd := &cobra.Command{
		Use:   "next",
		Short: "List ready tasks; with --claim, await + claim the first one",
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx, err := resolveAgentCtx(cmd)
			if err != nil {
				return err
			}
			// List the ready queue (Phase 15).
			raw, code, err := ctx.agentGet(cmd.Context(),
				"/api/v1/agent/tasks?ready=true&limit="+strconv.Itoa(limit))
			if err != nil {
				return err
			}
			if code != http.StatusOK {
				return fmt.Errorf("agent tasks: HTTP %d: %s", code, raw)
			}
			var resp struct {
				Tasks []struct {
					Task struct {
						ID     string `json:"id"`
						Number int    `json:"number"`
						Title  string `json:"title"`
					} `json:"task"`
					Ready bool `json:"ready"`
				} `json:"tasks"`
				Count int `json:"count"`
			}
			if err := json.Unmarshal(raw, &resp); err != nil {
				return err
			}
			if resp.Count == 0 {
				_, _ = cmd.OutOrStdout().Write([]byte("no work\n"))
				// Exit code 2 — convention from the spec.
				os.Exit(2)
			}
			first := resp.Tasks[0]
			if !first.Ready {
				// Shouldn't happen with ?ready=true, but defensive.
				_, _ = cmd.OutOrStdout().Write([]byte("no work\n"))
				os.Exit(2)
			}
			// Print the candidate, then claim it.
			printTaskRefHeader(cmd, first.Task.Number, first.Task.Title, first.Task.ID)
			if err := printJSON(cmd, first); err != nil {
				return err
			}
			claimRaw, claimCode, err := ctx.agentPost(cmd.Context(),
				"/api/v1/agent/tasks/"+first.Task.ID+"/claim", nil)
			if err != nil {
				return err
			}
			if claimCode != http.StatusOK {
				return fmt.Errorf("agent claim: HTTP %d: %s", claimCode, claimRaw)
			}
			// Echo the claim response so the agent sees the new state.
			_, _ = cmd.OutOrStdout().Write(claimRaw)
			_, _ = cmd.OutOrStdout().Write([]byte("\n"))
			return nil
		},
	}
	cmd.Flags().IntVar(&limit, "limit", 5, "max tasks to consider before claiming")
	cmd.Flags().IntVar(&awaitSecs, "await", 0, "if no work, long-poll up to N seconds (0 = no wait)")
	return cmd
}

// newAgentProposeCmd wires `orenda agent propose` (Phase 33.1).
//
// The DOGFOOD rule "new work = a task in the instance" is now
// executable by agents: propose creates a real task that lands as
// status=backlog + awaiting=human — the owner triages it from the
// review queue (accept = kanban-move to todo, dismiss = delete).
func newAgentProposeCmd() *cobra.Command {
	var (
		projectID   string
		title       string
		description string
		descFile    string
		priority    string
		blockedBy   string
		parentID    string
	)
	cmd := &cobra.Command{
		Use:   "propose",
		Short: "Propose a new task (lands in backlog, awaiting human triage)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if projectID == "" || title == "" {
				return fmt.Errorf("agent propose: --project and --title are required")
			}
			desc := description
			if descFile != "" {
				var (
					raw []byte
					err error
				)
				if descFile == "-" {
					raw, err = io.ReadAll(cmd.InOrStdin())
				} else {
					raw, err = os.ReadFile(descFile)
				}
				if err != nil {
					return fmt.Errorf("agent propose: read description: %w", err)
				}
				desc = string(raw)
			}
			if strings.TrimSpace(desc) == "" {
				return fmt.Errorf("agent propose: --description or --description-file is required")
			}
			body := map[string]any{
				"project_id":     projectID,
				"title":          title,
				"description_md": desc,
			}
			if priority != "" {
				body["priority"] = priority
			}
			if parentID != "" {
				body["parent_task_id"] = parentID
			}
			if blockedBy != "" {
				var ids []string
				for _, part := range strings.Split(blockedBy, ",") {
					if v := strings.TrimSpace(part); v != "" {
						ids = append(ids, v)
					}
				}
				if len(ids) > 0 {
					body["blocked_by"] = ids
				}
			}
			ctx, err := resolveAgentCtx(cmd)
			if err != nil {
				return err
			}
			raw, code, err := ctx.agentPost(cmd.Context(), "/api/v1/agent/tasks", body)
			if err != nil {
				return err
			}
			if code != http.StatusCreated {
				return fmt.Errorf("agent propose: HTTP %d: %s", code, raw)
			}
			var v any
			if err := json.Unmarshal(raw, &v); err != nil {
				return err
			}
			return printJSON(cmd, v)
		},
	}
	cmd.Flags().StringVar(&projectID, "project", "", "project id the task belongs to (required)")
	cmd.Flags().StringVar(&title, "title", "", "task title (required)")
	cmd.Flags().StringVar(&description, "description", "", "task description (markdown)")
	cmd.Flags().StringVar(&descFile, "description-file", "", "read markdown description from file ('-' = stdin)")
	cmd.Flags().StringVar(&priority, "priority", "", "low|medium|high|urgent (default medium)")
	cmd.Flags().StringVar(&blockedBy, "blocked-by", "", "comma-separated blocker task ids")
	cmd.Flags().StringVar(&parentID, "parent", "", "parent task id (creates a subtask)")
	return cmd
}

func newAgentContextCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "context <task-id|#N>",
		Short: "Fetch the full task context (task + comments + activity + children + checklists)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, err := resolveAgentCtx(cmd)
			if err != nil {
				return err
			}
			raw, code, err := ctx.agentGet(cmd.Context(),
				"/api/v1/agent/tasks/"+url.PathEscape(args[0])+"/context")
			if err != nil {
				return err
			}
			if code != http.StatusOK {
				return fmt.Errorf("agent context: HTTP %d: %s", code, raw)
			}
			// Header line carries the human number next to the UUID;
			// the full snapshot follows as JSON.
			var env struct {
				Task struct {
					ID     string `json:"id"`
					Number int    `json:"number"`
					Title  string `json:"title"`
				} `json:"task"`
			}
			if err := json.Unmarshal(raw, &env); err == nil {
				printTaskRefHeader(cmd, env.Task.Number, env.Task.Title, env.Task.ID)
			}
			var v any
			if err := json.Unmarshal(raw, &v); err != nil {
				return err
			}
			return printJSON(cmd, v)
		},
	}
}

func newAgentClaimCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "claim <task-id|#N>",
		Short: "Claim a task for the agent (409 if held, 422 if blocked)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, err := resolveAgentCtx(cmd)
			if err != nil {
				return err
			}
			raw, code, err := ctx.agentPost(cmd.Context(),
				"/api/v1/agent/tasks/"+url.PathEscape(args[0])+"/claim", nil)
			if err != nil {
				return err
			}
			if code != http.StatusOK {
				return fmt.Errorf("agent claim: HTTP %d: %s", code, raw)
			}
			var v any
			if err := json.Unmarshal(raw, &v); err != nil {
				return err
			}
			return printJSON(cmd, v)
		},
	}
}

func newAgentReleaseCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "release <task-id|#N>",
		Short: "Release a claim held by the agent",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, err := resolveAgentCtx(cmd)
			if err != nil {
				return err
			}
			raw, code, err := ctx.agentPost(cmd.Context(),
				"/api/v1/agent/tasks/"+url.PathEscape(args[0])+"/release", nil)
			if err != nil {
				return err
			}
			if code != http.StatusOK {
				return fmt.Errorf("agent release: HTTP %d: %s", code, raw)
			}
			var v any
			if err := json.Unmarshal(raw, &v); err != nil {
				return err
			}
			return printJSON(cmd, v)
		},
	}
}

func newAgentSubmitCmd() *cobra.Command {
	var note string
	cmd := &cobra.Command{
		Use:   "submit <task-id|#N>",
		Short: "Mark a claimed task ready for human review",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, err := resolveAgentCtx(cmd)
			if err != nil {
				return err
			}
			body := map[string]any{}
			if note != "" {
				body["note"] = note
			}
			raw, code, err := ctx.agentPost(cmd.Context(),
				"/api/v1/agent/tasks/"+url.PathEscape(args[0])+"/submit", body)
			if err != nil {
				return err
			}
			if code != http.StatusOK {
				return fmt.Errorf("agent submit: HTTP %d: %s", code, raw)
			}
			var v any
			if err := json.Unmarshal(raw, &v); err != nil {
				return err
			}
			return printJSON(cmd, v)
		},
	}
	cmd.Flags().StringVar(&note, "note", "", "optional note for the human reviewer")
	return cmd
}

func newAgentTimeCmd() *cobra.Command {
	var minutes float64
	cmd := &cobra.Command{
		Use:   "time <task-id|#N>",
		Short: "Log manual time on a task (minutes >= 0; 0 passes the submit gate)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, err := resolveAgentCtx(cmd)
			if err != nil {
				return err
			}
			body := map[string]any{"minutes": minutes}
			raw, code, err := ctx.agentPost(cmd.Context(),
				"/api/v1/agent/tasks/"+url.PathEscape(args[0])+"/time", body)
			if err != nil {
				return err
			}
			if code != http.StatusCreated {
				return fmt.Errorf("agent time: HTTP %d: %s", code, raw)
			}
			var v any
			if err := json.Unmarshal(raw, &v); err != nil {
				return err
			}
			return printJSON(cmd, v)
		},
	}
	cmd.Flags().Float64Var(&minutes, "minutes", 0, "minutes spent (>= 0; 0 marks the task time-tracked-trivial)")
	return cmd
}

func newAgentCommentCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "comment <task-id|#N> <body>",
		Short: "Add a comment to a task (markdown is supported)",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, err := resolveAgentCtx(cmd)
			if err != nil {
				return err
			}
			// Phase 27.11: post to the agent-namespace alias
			// `/api/v1/agent/tasks/{id}/comments` so the bearer
			// token resolves through RequireAgent (the user-side
			// route only accepts cookie/JWT sessions and would
			// 401 otherwise).
			raw, code, err := ctx.agentPost(cmd.Context(),
				"/api/v1/agent/tasks/"+url.PathEscape(args[0])+"/comments",
				map[string]any{"body_md": args[1]})
			if err != nil {
				return err
			}
			if code != http.StatusCreated && code != http.StatusOK {
				return fmt.Errorf("agent comment: HTTP %d: %s", code, raw)
			}
			var v any
			if err := json.Unmarshal(raw, &v); err != nil {
				return err
			}
			return printJSON(cmd, v)
		},
	}
}

// ---------------------------------------------------------------------------
// T96: checklist subcommands — the CLI twin of the agent-namespace
// checklist routes. The task argument accepts the UUID or the human
// number ("42" / "#42"), like every other `agent` task command.
// ---------------------------------------------------------------------------

func newAgentChecklistsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "checklists <task-id|#N>",
		Short: "List a task's checklists with their items",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, err := resolveAgentCtx(cmd)
			if err != nil {
				return err
			}
			raw, code, err := ctx.agentGet(cmd.Context(),
				"/api/v1/agent/tasks/"+url.PathEscape(args[0])+"/checklists")
			if err != nil {
				return err
			}
			if code != http.StatusOK {
				return fmt.Errorf("agent checklists: HTTP %d: %s", code, raw)
			}
			var v any
			if err := json.Unmarshal(raw, &v); err != nil {
				return err
			}
			return printJSON(cmd, v)
		},
	}
}

func newAgentChecklistAddCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "checklist-add <task-id|#N> <title>",
		Short: "Create a checklist on a task (lock holder only)",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, err := resolveAgentCtx(cmd)
			if err != nil {
				return err
			}
			raw, code, err := ctx.agentPost(cmd.Context(),
				"/api/v1/agent/tasks/"+url.PathEscape(args[0])+"/checklists",
				map[string]any{"title": args[1]})
			if err != nil {
				return err
			}
			if code != http.StatusCreated {
				return fmt.Errorf("agent checklist-add: HTTP %d: %s", code, raw)
			}
			var v any
			if err := json.Unmarshal(raw, &v); err != nil {
				return err
			}
			return printJSON(cmd, v)
		},
	}
}

func newAgentChecklistItemAddCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "checklist-item-add <task-id|#N> <checklist-id> <title>",
		Short: "Append an item to a checklist (lock holder only)",
		Args:  cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, err := resolveAgentCtx(cmd)
			if err != nil {
				return err
			}
			raw, code, err := ctx.agentPost(cmd.Context(),
				"/api/v1/agent/tasks/"+url.PathEscape(args[0])+"/checklists/"+url.PathEscape(args[1])+"/items",
				map[string]any{"title": args[2]})
			if err != nil {
				return err
			}
			if code != http.StatusCreated {
				return fmt.Errorf("agent checklist-item-add: HTTP %d: %s", code, raw)
			}
			var v any
			if err := json.Unmarshal(raw, &v); err != nil {
				return err
			}
			return printJSON(cmd, v)
		},
	}
}

func newAgentChecklistItemUpdateCmd() *cobra.Command {
	var (
		done  bool
		title string
	)
	cmd := &cobra.Command{
		Use:   "checklist-item-update <task-id|#N> <checklist-id> <item-id>",
		Short: "Update a checklist item: --done to tick, --title to rename (lock holder only)",
		Args:  cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, err := resolveAgentCtx(cmd)
			if err != nil {
				return err
			}
			body := map[string]any{}
			if cmd.Flags().Changed("done") {
				body["done"] = done
			}
			if title != "" {
				body["title"] = title
			}
			if len(body) == 0 {
				return fmt.Errorf("agent checklist-item-update: nothing to update — pass --done and/or --title")
			}
			raw, code, err := ctx.agentPatch(cmd.Context(),
				"/api/v1/agent/tasks/"+url.PathEscape(args[0])+"/checklists/"+url.PathEscape(args[1])+"/items/"+url.PathEscape(args[2]),
				body)
			if err != nil {
				return err
			}
			if code != http.StatusNoContent && code != http.StatusOK {
				return fmt.Errorf("agent checklist-item-update: HTTP %d: %s", code, raw)
			}
			if code == http.StatusNoContent {
				fmt.Fprintln(cmd.OutOrStdout(), "updated")
				return nil
			}
			var v any
			if err := json.Unmarshal(raw, &v); err != nil {
				return err
			}
			return printJSON(cmd, v)
		},
	}
	cmd.Flags().BoolVar(&done, "done", false, "set the item's done flag (pass --done=false to untick)")
	cmd.Flags().StringVar(&title, "title", "", "replace the item title")
	return cmd
}

func newAgentChecklistItemDeleteCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "checklist-item-delete <task-id|#N> <checklist-id> <item-id>",
		Short: "Delete a checklist item (lock holder only)",
		Args:  cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, err := resolveAgentCtx(cmd)
			if err != nil {
				return err
			}
			raw, code, err := ctx.agentDelete(cmd.Context(),
				"/api/v1/agent/tasks/"+url.PathEscape(args[0])+"/checklists/"+url.PathEscape(args[1])+"/items/"+url.PathEscape(args[2]))
			if err != nil {
				return err
			}
			if code != http.StatusNoContent && code != http.StatusOK {
				return fmt.Errorf("agent checklist-item-delete: HTTP %d: %s", code, raw)
			}
			fmt.Fprintln(cmd.OutOrStdout(), "deleted")
			return nil
		},
	}
}

// newAgentUpdateCmd wires `orenda agent update <task-id>` (Phase 33.2).
//
// Edit own un-triaged proposal (status=backlog + awaiting=human) or
// update agent_notes as the lock holder. Two paths:
//
//   - {agent_notes: "..."} only → holder gate
//   - any other field → proposal gate
//
// Permission failures surface as HTTP 403 with body {"error":
// "not_your_proposal"} / {"error": "not_lock_holder"}; the CLI
// propagates the HTTP status and body so the operator sees what
// went wrong.
func newAgentUpdateCmd() *cobra.Command {
	var (
		title, description, priority, dueAt, parentID, agentNotes string
	)
	cmd := &cobra.Command{
		Use:   "update <task-id|#N>",
		Short: "Edit own un-triaged proposal (title/description/priority/due_at/parent_task_id) or update agent_notes as the lock holder",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, err := resolveAgentCtx(cmd)
			if err != nil {
				return err
			}
			body := map[string]any{}
			if title != "" {
				body["title"] = title
			}
			if description != "" {
				body["description_md"] = description
			}
			if priority != "" {
				body["priority"] = priority
			}
			if dueAt != "" {
				body["due_at"] = dueAt
			}
			if parentID != "" {
				body["parent_task_id"] = parentID
			}
			if agentNotes != "" {
				body["agent_notes"] = agentNotes
			}
			if len(body) == 0 {
				return fmt.Errorf("agent update: no fields supplied (--title / --description / --priority / --due-at / --parent-task-id / --agent-notes)")
			}
			raw, code, err := ctx.agentPatch(cmd.Context(),
				"/api/v1/agent/tasks/"+url.PathEscape(args[0]), body)
			if err != nil {
				return err
			}
			if code != http.StatusOK {
				return fmt.Errorf("agent update: HTTP %d: %s", code, raw)
			}
			var v any
			if err := json.Unmarshal(raw, &v); err != nil {
				return err
			}
			return printJSON(cmd, v)
		},
	}
	cmd.Flags().StringVar(&title, "title", "", "new title")
	cmd.Flags().StringVar(&description, "description", "", "new description (markdown)")
	cmd.Flags().StringVar(&priority, "priority", "", "new priority (low|medium|high|urgent)")
	cmd.Flags().StringVar(&dueAt, "due-at", "", "new due date (RFC3339)")
	cmd.Flags().StringVar(&parentID, "parent-task-id", "", "re-parent under a different parent")
	cmd.Flags().StringVar(&agentNotes, "agent-notes", "", "set agent_notes (holder-only path)")
	return cmd
}

// newAgentRetractCmd wires `orenda agent retract <task-id>` (Phase 33.2).
//
// Hard-delete own un-triaged proposal. Same gate as PATCH: 403
// not_your_proposal when the task is triaged or authored by
// someone else. Success is HTTP 204 (no body).
func newAgentRetractCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "retract <task-id|#N>",
		Short: "Hard-delete own un-triaged proposal",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, err := resolveAgentCtx(cmd)
			if err != nil {
				return err
			}
			raw, code, err := ctx.agentDelete(cmd.Context(),
				"/api/v1/agent/tasks/"+url.PathEscape(args[0]))
			if err != nil {
				return err
			}
			if code != http.StatusNoContent && code != http.StatusOK {
				return fmt.Errorf("agent retract: HTTP %d: %s", code, raw)
			}
			if code == http.StatusNoContent {
				fmt.Fprintln(cmd.OutOrStdout(), "retracted")
				return nil
			}
			var v any
			if err := json.Unmarshal(raw, &v); err != nil {
				return err
			}
			return printJSON(cmd, v)
		},
	}
}

// newAgentPRWatchCmd — Phase 32.10 pr-watch helper. The harness
// agent polls GitHub (via the `gh` CLI) for PR events and uses
// this subcommand to leave a trace on the linked Orenda task. The
// flow is intentionally one-shot per call — the harness is
// responsible for the polling loop (every N minutes, per task).
//
// Why polling via `gh` instead of a GitHub webhook:
//   - Local-first install behind NAT — can't accept inbound webhooks.
//   - `gh` is already in the harness's toolchain (it manages the
//     dogfood's repo today).
//   - One-shot CLI is composable; the harness can run it from any
//     scheduler, sidecar, or cron.
//
// Wire shape:
//
//	orenda agent pr-watch <task-id> [--repo owner/name] [--dry]
//
// Behaviour:
//  1. Fetch the task via /api/v1/agent/tasks/{id}/context.
//  2. Parse a PR number from the description (formats: "closes #N",
//     "refs #N", "PR: <owner/name>#N", or a bare "<owner/name>#N").
//     First match wins; explicit --repo + --number override.
//  3. Run `gh pr view <number> --repo <repo> --json state,title,headRefName,mergeCommit`.
//  4. Build a markdown status line and POST it via
func newAgentAwaitCmd() *cobra.Command {
	var timeoutSecs int
	cmd := &cobra.Command{
		Use:   "await",
		Short: "Long-poll for the next event (task.created, task.reviewed, etc.)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx, err := resolveAgentCtx(cmd)
			if err != nil {
				return err
			}
			// Phase 27.11: post to the agent-namespace alias
			// `/api/v1/agent/events/await` so the bearer token
			// resolves through RequireAgent. The handler reads
			// Identity.AgentID and subscribes the hub under
			// that id; user-side /events/await only accepts
			// cookie/JWT and would 401 otherwise.
			body := map[string]any{
				"timeout_s": timeoutSecs,
			}
			raw, code, err := ctx.agentPost(cmd.Context(),
				"/api/v1/agent/events/await", body)
			if err != nil {
				return err
			}
			// 204 No Content = timeout, no events; exit 2.
			if code == http.StatusNoContent {
				_, _ = cmd.OutOrStdout().Write([]byte("timeout\n"))
				os.Exit(2)
			}
			if code != http.StatusOK {
				return fmt.Errorf("agent await: HTTP %d: %s", code, raw)
			}
			var v any
			if err := json.Unmarshal(raw, &v); err != nil {
				return err
			}
			return printJSON(cmd, v)
		},
	}
	cmd.Flags().IntVar(&timeoutSecs, "timeout", 30, "long-poll timeout in seconds (max 60)")
	_ = time.Duration(0)
	return cmd
}

// ---------------------------------------------------------------------------
// Phase 29.2: wiki + search surface for the agent CLI.
// ---------------------------------------------------------------------------

// newAgentPagesCmd wires `orenda agent pages <list|get|put|delete|move|backlinks>`.
// The wiki endpoints live in the agent namespace (Phase 29.1); the
// CLI is a thin shim over them, same -json contract as the rest of
// the tree.
func newAgentPagesCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "pages",
		Short: "Manage wiki pages and blocks (list/get/put/delete/move/backlinks/blocks)",
	}
	cmd.AddCommand(newAgentPagesListCmd())
	cmd.AddCommand(newAgentPagesGetCmd())
	cmd.AddCommand(newAgentPagesPutCmd())
	cmd.AddCommand(newAgentPagesDeleteCmd())
	cmd.AddCommand(newAgentPagesMoveCmd())
	cmd.AddCommand(newAgentPagesBacklinksCmd())
	cmd.AddCommand(newAgentPagesBlocksCmd())
	return cmd
}

// agentPagesGet is the shared GET-and-print helper for the pages
// subcommands.
func agentPagesGet(cmd *cobra.Command, path string, okCodes ...int) error {
	ctx, err := resolveAgentCtx(cmd)
	if err != nil {
		return err
	}
	raw, code, err := ctx.agentGet(cmd.Context(), path)
	if err != nil {
		return err
	}
	ok := false
	for _, c := range okCodes {
		if code == c {
			ok = true
			break
		}
	}
	if !ok {
		return fmt.Errorf("agent pages: HTTP %d: %s", code, raw)
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return err
	}
	return printJSON(cmd, v)
}

func newAgentPagesListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "Print the wiki page tree",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return agentPagesGet(cmd, "/api/v1/agent/pages", http.StatusOK)
		},
	}
}

func newAgentPagesGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get <slug>",
		Short: "Fetch a wiki page by slug",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return agentPagesGet(cmd, "/api/v1/agent/pages/"+url.PathEscape(args[0]), http.StatusOK)
		},
	}
}

func newAgentPagesPutCmd() *cobra.Command {
	var (
		title    string
		file     string
		parentID string
	)
	cmd := &cobra.Command{
		Use:   "put <slug>",
		Short: "Create or update a wiki page (markdown from --file or stdin)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, err := resolveAgentCtx(cmd)
			if err != nil {
				return err
			}
			var content []byte
			if file != "" && file != "-" {
				content, err = os.ReadFile(file)
				if err != nil {
					return fmt.Errorf("agent pages put: read %s: %w", file, err)
				}
			} else {
				content, err = io.ReadAll(cmd.InOrStdin())
				if err != nil {
					return fmt.Errorf("agent pages put: read stdin: %w", err)
				}
			}
			body := map[string]any{
				"slug":       args[0],
				"title":      title,
				"content_md": string(content),
			}
			if parentID != "" {
				body["parent_id"] = parentID
			}
			raw, mErr := json.Marshal(body)
			if mErr != nil {
				return mErr
			}
			respBody, code, err := ctx.doRaw(cmd.Context(), http.MethodPut,
				"/api/v1/agent/pages/"+url.PathEscape(args[0]),
				bytes.NewReader(raw), "application/json")
			if err != nil {
				return err
			}
			if code != http.StatusOK && code != http.StatusCreated {
				return fmt.Errorf("agent pages put: HTTP %d: %s", code, respBody)
			}
			var v any
			if err := json.Unmarshal(respBody, &v); err != nil {
				return err
			}
			return printJSON(cmd, v)
		},
	}
	cmd.Flags().StringVar(&title, "title", "", "page title (defaults to the slug)")
	cmd.Flags().StringVar(&file, "file", "", "markdown source file ('-' or empty = stdin)")
	cmd.Flags().StringVar(&parentID, "parent", "", "parent page id (empty = root)")
	return cmd
}

func newAgentPagesDeleteCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "delete <slug>",
		Short: "Delete a wiki page (children are cascade-deleted by the service)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, err := resolveAgentCtx(cmd)
			if err != nil {
				return err
			}
			raw, code, err := ctx.do(cmd.Context(), http.MethodDelete,
				"/api/v1/agent/pages/"+url.PathEscape(args[0]), nil)
			if err != nil {
				return err
			}
			if code != http.StatusNoContent && code != http.StatusOK {
				return fmt.Errorf("agent pages delete: HTTP %d: %s", code, raw)
			}
			_, _ = cmd.OutOrStdout().Write([]byte("deleted\n"))
			return nil
		},
	}
}

func newAgentPagesMoveCmd() *cobra.Command {
	var parentID string
	cmd := &cobra.Command{
		Use:   "move <slug>",
		Short: "Move a wiki page under a new parent (empty --parent = root)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, err := resolveAgentCtx(cmd)
			if err != nil {
				return err
			}
			var rdr io.Reader
			raw, mErr := json.Marshal(map[string]any{"parent_id": parentID})
			if mErr != nil {
				return mErr
			}
			rdr = bytes.NewReader(raw)
			resp, code, err := ctx.doRaw(cmd.Context(), http.MethodPatch,
				"/api/v1/agent/pages/"+url.PathEscape(args[0])+"/move", rdr, "application/json")
			if err != nil {
				return err
			}
			if code != http.StatusNoContent && code != http.StatusOK {
				return fmt.Errorf("agent pages move: HTTP %d: %s", code, resp)
			}
			_, _ = cmd.OutOrStdout().Write([]byte("moved\n"))
			return nil
		},
	}
	cmd.Flags().StringVar(&parentID, "parent", "", "new parent page id (empty = root)")
	return cmd
}

func newAgentPagesBacklinksCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "backlinks <slug>",
		Short: "List pages that link to <slug>",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return agentPagesGet(cmd,
				"/api/v1/agent/pages/"+url.PathEscape(args[0])+"/backlinks", http.StatusOK)
		},
	}
}

// newAgentPagesBlocksCmd wires `orenda agent pages blocks <get|put>`
// — the CLI shim over GET/PUT /api/v1/agent/pages/{slug}/blocks.
// The server resolves {slug} as a slug OR a W<N> reference, so the
// CLI passes the argument through verbatim (PathEscape'd).
func newAgentPagesBlocksCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "blocks",
		Short: "Read and replace the block tree of a page (get/put)",
	}
	cmd.AddCommand(newAgentPagesBlocksGetCmd())
	cmd.AddCommand(newAgentPagesBlocksPutCmd())
	return cmd
}

func newAgentPagesBlocksGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get <slug>",
		Short: "Print the block view of a page (slug or W<N> reference)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return agentPagesGet(cmd,
				"/api/v1/agent/pages/"+url.PathEscape(args[0])+"/blocks", http.StatusOK)
		},
	}
}

func newAgentPagesBlocksPutCmd() *cobra.Command {
	var file string
	cmd := &cobra.Command{
		Use:   "put <slug>",
		Short: "Replace the block tree of a page (block JSON from --file or stdin)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, err := resolveAgentCtx(cmd)
			if err != nil {
				return err
			}
			var content []byte
			if file != "" && file != "-" {
				content, err = os.ReadFile(file)
				if err != nil {
					return fmt.Errorf("agent pages blocks put: read %s: %w", file, err)
				}
			} else {
				content, err = io.ReadAll(cmd.InOrStdin())
				if err != nil {
					return fmt.Errorf("agent pages blocks put: read stdin: %w", err)
				}
			}
			var blocks []any
			if err := json.Unmarshal(content, &blocks); err != nil {
				return fmt.Errorf("agent pages blocks put: body must be a JSON array of blocks: %w", err)
			}
			body, _ := json.Marshal(map[string]any{"blocks": blocks})
			respBody, code, err := ctx.doRaw(cmd.Context(), http.MethodPut,
				"/api/v1/agent/pages/"+url.PathEscape(args[0])+"/blocks",
				bytes.NewReader(body), "application/json")
			if err != nil {
				return err
			}
			if code != http.StatusOK && code != http.StatusCreated {
				return fmt.Errorf("agent pages blocks put: HTTP %d: %s", code, respBody)
			}
			var v any
			if err := json.Unmarshal(respBody, &v); err != nil {
				return err
			}
			return printJSON(cmd, v)
		},
	}
	cmd.Flags().StringVar(&file, "file", "", "JSON block-array file ('-' or empty = stdin)")
	return cmd
}

func newAgentSearchCmd() *cobra.Command {
	var (
		typ   string
		limit int
	)
	cmd := &cobra.Command{
		Use:   "search <query>",
		Short: "Full-text search across pages, tasks, and comments",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			q := url.Values{}
			q.Set("q", args[0])
			if typ != "" {
				q.Set("type", typ)
			}
			if limit > 0 {
				q.Set("limit", strconv.Itoa(limit))
			}
			return agentPagesGet(cmd, "/api/v1/agent/search?"+q.Encode(), http.StatusOK)
		},
	}
	cmd.Flags().StringVar(&typ, "type", "", "restrict to a hit type (page|task|comment)")
	cmd.Flags().IntVar(&limit, "limit", 0, "max hits (0 = server default)")
	return cmd
}

// newAgentCoursesCmd wires `orenda agent courses list`.
//
// Phase 31.8: the planner reads the active-course list to know
// what to propose. With --status active the response carries
// progress + pace notes for each course so the planner doesn't
// have to round-trip per-course.
func newAgentCoursesCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "courses",
		Short: "Course-related agent commands",
	}
	cmd.AddCommand(newAgentCoursesListCmd())
	return cmd
}

func newAgentCoursesListCmd() *cobra.Command {
	var status string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List courses (active = progress + pace notes enriched)",
		RunE: func(cmd *cobra.Command, args []string) error {
			path := "/api/v1/agent/courses"
			if status != "" {
				path += "?status=" + url.QueryEscape(status)
			}
			return agentPagesGet(cmd, path, http.StatusOK)
		},
	}
	cmd.Flags().StringVar(&status, "status", "", "filter by status (draft|review|active|done|archived)")
	return cmd
}

// newAgentStudyProposeCmd wires `orenda agent study-propose`.
//
// Phase 31.8: the planner's only study-side surface. Files a
// pending proposal that the user can accept or dismiss from the
// Dashboard tray.
func newAgentStudyProposeCmd() *cobra.Command {
	var (
		courseID   string
		title      string
		bodyMD     string
		targetDate string
	)
	cmd := &cobra.Command{
		Use:   "study-propose",
		Short: "File a pending study proposal (the Dashboard tray picks it up)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if title == "" || targetDate == "" {
				return fmt.Errorf("study-propose: --title and --target-date are required")
			}
			body := map[string]string{
				"title":       title,
				"target_date": targetDate,
			}
			if courseID != "" {
				body["course_id"] = courseID
			}
			if bodyMD != "" {
				body["body_md"] = bodyMD
			}
			ctx, err := resolveAgentCtx(cmd)
			if err != nil {
				return err
			}
			raw, code, err := ctx.agentPost(cmd.Context(),
				"/api/v1/agent/study-proposals", body)
			if err != nil {
				return err
			}
			if code != http.StatusCreated {
				return fmt.Errorf("agent study-propose: HTTP %d: %s", code, raw)
			}
			var v any
			if err := json.Unmarshal(raw, &v); err != nil {
				return err
			}
			return printJSON(cmd, v)
		},
	}
	cmd.Flags().StringVar(&courseID, "course-id", "", "optional course id (omit for a free-standing reminder)")
	cmd.Flags().StringVar(&title, "title", "", "proposal title (required)")
	cmd.Flags().StringVar(&bodyMD, "body-md", "", "optional markdown body")
	cmd.Flags().StringVar(&targetDate, "target-date", "", "YYYY-MM-DD (required)")
	return cmd
}

// newAgentPRWatchCmd — Phase 32.10 "Синхронизация задача ↔ PR".
//
// The task description often carries a PR reference ("PR #N", "closes #N",
// "refs #N", or a bare "#N"). This subcommand reads the task, extracts
// the PR number, calls `gh pr view` to fetch the current state, and
// prints a JSON blob the harness can consume to decide whether to
// post a comment to the task. The harness is responsible for the
// polling loop and the dedup logic (compare against the last-known
// PR state it stored locally); orenda doesn't run a daemon.
//
// Why a one-shot CLI instead of a server endpoint:
//   - local-first install behind NAT, no inbound webhooks
//   - the harness already has `gh` credentials and polls anyway
//   - a one-shot command is cheap to test and easy to call from cron
//
// See wiki:pr-sync for the full decision and the harness-side
// polling pattern.
func newAgentPRWatchCmd() *cobra.Command {
	var (
		repoFlag string // optional "owner/name"; defaults to gh's auto-detection
		number   int    // PR number to fetch
	)
	cmd := &cobra.Command{
		Use:   "pr-watch <task-id>",
		Short: "Fetch the current state of a PR linked to a task (harness-side sync)",
		Long: `Reads the task, extracts the PR number from its description
(PR #N / closes #N / refs #N), runs gh pr view, and prints the
state as JSON. The harness posts a comment to the task when the
state changes; orenda itself doesn't run a daemon.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, err := resolveAgentCtx(cmd)
			if err != nil {
				return err
			}
			taskID := args[0]

			// Step 1: fetch the task so we can read the description.
			raw, code, err := ctx.agentGet(cmd.Context(),
				"/api/v1/agent/tasks/"+url.PathEscape(taskID)+"/context")
			if err != nil {
				return fmt.Errorf("pr-watch: get task: %w", err)
			}
			if code != http.StatusOK {
				return fmt.Errorf("pr-watch: HTTP %d: %s", code, raw)
			}
			// The context endpoint returns {task, comments, activity,
			// children, checklists}; we only need the task description.
			var env struct {
				Task struct {
					Description string `json:"description"`
				} `json:"task"`
			}
			if err := json.Unmarshal(raw, &env); err != nil {
				return fmt.Errorf("pr-watch: decode task: %w", err)
			}

			// Step 2: extract the PR number from the description if the
			// caller didn't pass --number. The harness typically
			// passes --number explicitly (it already parsed the
			// description to decide this task needs a sync).
			if number == 0 {
				n, ok := prNumberFromDescription(env.Task.Description)
				if !ok {
					return fmt.Errorf("pr-watch: no PR reference in task description; pass --number to override")
				}
				number = n
			}

			// Step 3: gh pr view --json state,title,number,headRefName,url,mergedAt
			stateJSON, err := runGHPrView(cmd.Context(), repoFlag, number)
			if err != nil {
				return fmt.Errorf("pr-watch: gh pr view: %w", err)
			}
			var payload map[string]any
			if err := json.Unmarshal(stateJSON, &payload); err != nil {
				return fmt.Errorf("pr-watch: decode gh output: %w", err)
			}
			payload["task_id"] = taskID
			return printJSON(cmd, payload)
		},
	}
	cmd.Flags().StringVar(&repoFlag, "repo", "", "optional owner/name override (defaults to gh's auto-detection)")
	cmd.Flags().IntVar(&number, "number", 0, "PR number (extracted from task description when omitted)")
	return cmd
}

// prNumberFromDescription pulls the first PR-like reference out of
// a task description. Supports "PR #N", "closes #N", "refs #N", and
// a bare "#N". Returns (0, false) when nothing matches.
//
// Phase 32.10 deliberately keeps this naive (regex over the whole
// description, first match wins). The convention enforced by the
// PR template is "PR #N in the description", so the first #N we
// see is the right one in practice.
func prNumberFromDescription(desc string) (int, bool) {
	re := regexp.MustCompile(`(?i)(?:PR|closes|refs|fixes)\s*#(\d+)|^#(\d+)\b`)
	m := re.FindStringSubmatch(desc)
	if m == nil {
		return 0, false
	}
	for _, g := range m[1:] {
		if g != "" {
			n, err := strconv.Atoi(g)
			if err != nil {
				return 0, false
			}
			return n, true
		}
	}
	return 0, false
}

// runGHPrView shells out to the gh CLI to fetch the PR's current
// state. Returns the raw JSON. The harness is responsible for
// configuring gh's auth (this binary doesn't ship creds).
func runGHPrView(ctx context.Context, repo string, number int) ([]byte, error) {
	args := []string{"pr", "view", strconv.Itoa(number), "--json",
		"state,title,number,headRefName,url,mergedAt,additions,deletions,number"}
	if repo != "" {
		args = append([]string{"pr", "view", strconv.Itoa(number), "--repo", repo, "--json",
			"state,title,number,headRefName,url,mergedAt"}, args[6:]...)
	}
	cmd := exec.CommandContext(ctx, "gh", args...)
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("gh: %w (output: %s)", err, string(out))
	}
	return out, nil
}
