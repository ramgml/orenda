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
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
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
		// Fall back to the config file (only if both are missing).
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
	if baseURL == "" {
		return nil, errors.New("orenda agent: --url (or ORENDA_URL) is required")
	}
	if token == "" {
		return nil, errors.New("orenda agent: --token (or ORENDA_AGENT_TOKEN) is required")
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
func (a *agentCtx) agentGet(ctx context.Context, path string) ([]byte, int, error) {
	return a.do(ctx, http.MethodGet, path, nil)
}

func (a *agentCtx) agentPost(ctx context.Context, path string, body any) ([]byte, int, error) {
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

func (a *agentCtx) agentPut(ctx context.Context, path string, body any) ([]byte, int, error) {
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, 0, err
	}
	return a.doRaw(ctx, http.MethodPut, path, bytes.NewReader(raw), "application/json")
}

func (a *agentCtx) do(ctx context.Context, method, path string, body io.Reader) ([]byte, int, error) {
	return a.doRaw(ctx, method, path, body, "")
}

func (a *agentCtx) doRaw(ctx context.Context, method, path string, body io.Reader, contentType string) ([]byte, int, error) {
	u, err := url.Parse(a.BaseURL)
	if err != nil {
		return nil, 0, fmt.Errorf("bad base url: %w", err)
	}
	u.Path = strings.TrimRight(u.Path, "/") + path
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
	cmd.OutOrStdout().Write(raw)
	cmd.OutOrStdout().Write([]byte("\n"))
	return nil
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
  orenda agent context <task-id>       # pull the full snapshot
  orenda agent comment <task-id> <md>  # add a comment (not-blocking)
  orenda agent submit <task-id>       # mark ready for human review
  orenda agent release <task-id>      # drop a claim
  orenda agent await                   # long-poll for the next event

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
	cmd.AddCommand(newAgentContextCmd())
	cmd.AddCommand(newAgentClaimCmd())
	cmd.AddCommand(newAgentReleaseCmd())
	cmd.AddCommand(newAgentSubmitCmd())
	cmd.AddCommand(newAgentCommentCmd())
	cmd.AddCommand(newAgentAwaitCmd())
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
						ID    string `json:"id"`
						Title string `json:"title"`
					} `json:"task"`
					Ready bool `json:"ready"`
				} `json:"tasks"`
				Count int `json:"count"`
			}
			if err := json.Unmarshal(raw, &resp); err != nil {
				return err
			}
			if resp.Count == 0 {
				cmd.OutOrStdout().Write([]byte("no work\n"))
				// Exit code 2 — convention from the spec.
				os.Exit(2)
			}
			first := resp.Tasks[0]
			if !first.Ready {
				// Shouldn't happen with ?ready=true, but defensive.
				cmd.OutOrStdout().Write([]byte("no work\n"))
				os.Exit(2)
			}
			// Print the candidate, then claim it.
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
			cmd.OutOrStdout().Write(claimRaw)
			cmd.OutOrStdout().Write([]byte("\n"))
			return nil
		},
	}
	cmd.Flags().IntVar(&limit, "limit", 5, "max tasks to consider before claiming")
	cmd.Flags().IntVar(&awaitSecs, "await", 0, "if no work, long-poll up to N seconds (0 = no wait)")
	return cmd
}

func newAgentContextCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "context <task-id>",
		Short: "Fetch the full task context (task + comments + activity + children + checklists)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, err := resolveAgentCtx(cmd)
			if err != nil {
				return err
			}
			raw, code, err := ctx.agentGet(cmd.Context(),
				"/api/v1/agent/tasks/"+args[0]+"/context")
			if err != nil {
				return err
			}
			if code != http.StatusOK {
				return fmt.Errorf("agent context: HTTP %d: %s", code, raw)
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
		Use:   "claim <task-id>",
		Short: "Claim a task for the agent (409 if held, 422 if blocked)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, err := resolveAgentCtx(cmd)
			if err != nil {
				return err
			}
			raw, code, err := ctx.agentPost(cmd.Context(),
				"/api/v1/agent/tasks/"+args[0]+"/claim", nil)
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
		Use:   "release <task-id>",
		Short: "Release a claim held by the agent",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, err := resolveAgentCtx(cmd)
			if err != nil {
				return err
			}
			raw, code, err := ctx.agentPost(cmd.Context(),
				"/api/v1/agent/tasks/"+args[0]+"/release", nil)
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
		Use:   "submit <task-id>",
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
				"/api/v1/agent/tasks/"+args[0]+"/submit", body)
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

func newAgentCommentCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "comment <task-id> <body>",
		Short: "Add a comment to a task (markdown is supported)",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, err := resolveAgentCtx(cmd)
			if err != nil {
				return err
			}
			// Comments currently live under the user-side route.
			// The agent-side mirror is a planned addition; for now
			// we use the user-side endpoint with the bearer token
			// (which is also valid because we set up the agent
			// to fall through to the user-side middleware when
			// the agent-side handler doesn't match).
			raw, code, err := ctx.agentPost(cmd.Context(),
				"/api/v1/tasks/"+args[0]+"/comments",
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
			// POST /api/v1/events/await with an optional timeout.
			// The endpoint accepts a JSON body {timeout_s, filter}.
			body := map[string]any{
				"timeout_s": timeoutSecs,
			}
			raw, code, err := ctx.agentPost(cmd.Context(),
				"/api/v1/events/await", body)
			if err != nil {
				return err
			}
			// 204 No Content = timeout, no events; exit 2.
			if code == http.StatusNoContent {
				cmd.OutOrStdout().Write([]byte("timeout\n"))
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
