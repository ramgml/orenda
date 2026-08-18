package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// Phase 25: smoke tests for the agent CLI's transport layer.
//
// We don't exec the binary — the cobra surface is exercised by the
// hand-rolled resolveAgent helper and the agentCtx's HTTP methods
// against an httptest server. The end-to-end binary test is the
// build + smoke (./orenda agent --help).

func newAgentCtx(t *testing.T, h http.Handler) *agentCtx {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return &agentCtx{BaseURL: srv.URL, Token: "test-token"}
}

func TestAgentMe_OK(t *testing.T) {
	ctx := newAgentCtx(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "Bearer test-token", r.Header.Get("Authorization"))
		assert.Equal(t, "/api/v1/agent/me", r.URL.Path)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"a-1","name":"qa-bot","status":"online"}`))
	}))
	raw, code, err := ctx.agentGet(context.Background(), "/api/v1/agent/me")
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, code)
	var got map[string]any
	require.NoError(t, json.Unmarshal(raw, &got))
	assert.Equal(t, "qa-bot", got["name"])
}

func TestAgentMe_404(t *testing.T) {
	ctx := newAgentCtx(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"not_found"}`))
	}))
	_, code, err := ctx.agentGet(context.Background(), "/api/v1/agent/me")
	require.NoError(t, err)
	assert.Equal(t, http.StatusNotFound, code)
}

func TestAgentPost_SendsBody(t *testing.T) {
	ctx := newAgentCtx(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/v1/agent/tasks/abc/claim", r.URL.Path)
		assert.Equal(t, "POST", r.Method)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"abc","status":"in_progress"}`))
	}))
	raw, code, err := ctx.agentPost(context.Background(),
		"/api/v1/agent/tasks/abc/claim", nil)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, code)
	var got map[string]any
	require.NoError(t, json.Unmarshal(raw, &got))
	assert.Equal(t, "in_progress", got["status"])
}

// resolveAgentCtx precedence: flag > env > config file.
//
// We use AgentCLI for tests so we can inject a fake command without
// pulling cobra into the test scaffolding.

type agentCLIOptions struct {
	URL   string
	Token string
}

func resolveAgentCtxForTest(opts agentCLIOptions) (*agentCtx, error) {
	url := opts.URL
	token := opts.Token
	if url == "" {
		url = os.Getenv("ORENDA_URL")
	}
	if token == "" {
		token = os.Getenv("ORENDA_AGENT_TOKEN")
	}
	if url == "" || token == "" {
		path, err := agentConfigPath()
		if err == nil {
			cfg, err := loadAgentConfig(path)
			if err == nil {
				if url == "" {
					url = cfg.URL
				}
				if token == "" {
					token = cfg.Token
				}
			}
		}
	}
	if url == "" {
		return nil, errors.New("orenda agent: --url (or ORENDA_URL) is required")
	}
	if token == "" {
		return nil, errors.New("orenda agent: --token (or ORENDA_AGENT_TOKEN) is required")
	}
	return &agentCtx{BaseURL: url, Token: token}, nil
}

func TestResolveAgentCtx_ConfigFile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("ORENDA_URL", "")
	t.Setenv("ORENDA_AGENT_TOKEN", "")

	raw, err := yaml.Marshal(agentConfig{URL: "http://from-config", Token: "tok-from-config"})
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "orenda"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "orenda", "agent.yaml"), raw, 0o600))

	got, err := resolveAgentCtxForTest(agentCLIOptions{})
	require.NoError(t, err)
	assert.Equal(t, "http://from-config", got.BaseURL)
	assert.Equal(t, "tok-from-config", got.Token)
}

func TestResolveAgentCtx_Env(t *testing.T) {
	t.Setenv("ORENDA_URL", "http://from-env")
	t.Setenv("ORENDA_AGENT_TOKEN", "tok-from-env")
	got, err := resolveAgentCtxForTest(agentCLIOptions{})
	require.NoError(t, err)
	assert.Equal(t, "http://from-env", got.BaseURL)
	assert.Equal(t, "tok-from-env", got.Token)
}

func TestResolveAgentCtx_Flag(t *testing.T) {
	t.Setenv("ORENDA_URL", "")
	t.Setenv("ORENDA_AGENT_TOKEN", "")
	got, err := resolveAgentCtxForTest(agentCLIOptions{
		URL: "http://from-flag", Token: "tok-from-flag",
	})
	require.NoError(t, err)
	assert.Equal(t, "http://from-flag", got.BaseURL)
	assert.Equal(t, "tok-from-flag", got.Token)
}

func TestResolveAgentCtx_Missing(t *testing.T) {
	t.Setenv("ORENDA_URL", "")
	t.Setenv("ORENDA_AGENT_TOKEN", "")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	_, err := resolveAgentCtxForTest(agentCLIOptions{})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	t.Logf("error: %s", err.Error())
	// Accept either the URL-missing error or the token-missing
	// error (the URL check fires first).
	if !strings.Contains(err.Error(), "--url") && !strings.Contains(err.Error(), "--token") {
		t.Errorf("error should mention --url or --token, got: %s", err.Error())
	}
}

// ---------------------------------------------------------------------------
// Phase 29.2: pages/search subcommands against an httptest server.
// The full cobra tree is executed so flag inheritance and arg
// parsing are exercised, not just the transport.
// ---------------------------------------------------------------------------

// runAgentCLI executes the agent command tree with the global
// --url/--token flags pointed at srv and returns stdout.
func runAgentCLI(t *testing.T, srv *httptest.Server, args ...string) (string, error) {
	t.Helper()
	root := newAgentCmd()
	var out strings.Builder
	root.SetOut(&out)
	full := append([]string{"--url", srv.URL, "--token", "test-token"}, args...)
	root.SetArgs(full)
	err := root.Execute()
	return out.String(), err
}

func TestAgentPagesPut_SendsPUTWithMarkdown(t *testing.T) {
	md := "# Hello\n\nSee [[other-page]].\n"
	mdFile := filepath.Join(t.TempDir(), "page.md")
	require.NoError(t, os.WriteFile(mdFile, []byte(md), 0o600))

	var gotMethod, gotPath string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		assert.Equal(t, "Bearer test-token", r.Header.Get("Authorization"))
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"p-1","slug":"my-page","title":"My Page"}`))
	}))
	t.Cleanup(srv.Close)

	out, err := runAgentCLI(t, srv, "pages", "put", "my-page", "--title", "My Page", "--file", mdFile)
	require.NoError(t, err)
	assert.Equal(t, http.MethodPut, gotMethod, "pages put must use PUT (the endpoint is an upsert)")
	assert.Equal(t, "/api/v1/agent/pages/my-page", gotPath)
	assert.Equal(t, "My Page", gotBody["title"])
	assert.Equal(t, md, gotBody["content_md"])
	assert.Contains(t, out, "my-page")
}

func TestAgentPagesPut_ReadsStdin(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"p-2","slug":"stdin-page"}`))
	}))
	t.Cleanup(srv.Close)

	root := newAgentCmd()
	var out strings.Builder
	root.SetOut(&out)
	root.SetIn(strings.NewReader("from stdin\n"))
	root.SetArgs([]string{"--url", srv.URL, "--token", "test-token",
		"pages", "put", "stdin-page", "--title", "Stdin"})
	require.NoError(t, root.Execute())
	assert.Equal(t, "from stdin\n", gotBody["content_md"])
}

func TestAgentPagesMove_SendsPATCH(t *testing.T) {
	var gotMethod, gotPath string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(srv.Close)

	out, err := runAgentCLI(t, srv, "pages", "move", "child-page", "--parent", "parent-id-1")
	require.NoError(t, err)
	assert.Equal(t, http.MethodPatch, gotMethod)
	assert.Equal(t, "/api/v1/agent/pages/child-page/move", gotPath)
	assert.Equal(t, "parent-id-1", gotBody["parent_id"])
	assert.Contains(t, out, "moved")
}

func TestAgentSearch_EncodesQuery(t *testing.T) {
	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"hits":[]}`))
	}))
	t.Cleanup(srv.Close)

	_, err := runAgentCLI(t, srv, "search", "hello world", "--type", "page", "--limit", "5")
	require.NoError(t, err)
	assert.Contains(t, gotQuery, "q=hello+world")
	assert.Contains(t, gotQuery, "type=page")
	assert.Contains(t, gotQuery, "limit=5")
}

// ---------------------------------------------------------------------------
// Phase 33.1: `agent propose` — the CLI twin of POST /api/v1/agent/tasks.
// ---------------------------------------------------------------------------

func TestAgentPropose_PostsToAgentTasks(t *testing.T) {
	md := "# Spec\n\nAgents need to file work themselves.\n"
	mdFile := filepath.Join(t.TempDir(), "task.md")
	require.NoError(t, os.WriteFile(mdFile, []byte(md), 0o600))

	var gotMethod, gotPath string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		assert.Equal(t, "Bearer test-token", r.Header.Get("Authorization"))
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"t-9","status":"backlog","awaiting":"human"}`))
	}))
	t.Cleanup(srv.Close)

	out, err := runAgentCLI(t, srv, "propose",
		"--project", "p-1", "--title", "File work",
		"--description-file", mdFile,
		"--priority", "high", "--blocked-by", "t-1, t-2", "--parent", "t-parent")
	require.NoError(t, err)
	assert.Equal(t, http.MethodPost, gotMethod)
	assert.Equal(t, "/api/v1/agent/tasks", gotPath)
	assert.Equal(t, "p-1", gotBody["project_id"])
	assert.Equal(t, "File work", gotBody["title"])
	assert.Equal(t, md, gotBody["description_md"])
	assert.Equal(t, "high", gotBody["priority"])
	assert.Equal(t, []any{"t-1", "t-2"}, gotBody["blocked_by"])
	assert.Equal(t, "t-parent", gotBody["parent_task_id"])
	assert.Contains(t, out, "t-9")
}

func TestAgentPropose_RequiresFlags(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("server must not be hit on client-side validation failure")
		w.WriteHeader(http.StatusCreated)
	}))
	t.Cleanup(srv.Close)

	// Missing --project.
	_, err := runAgentCLI(t, srv, "propose", "--title", "T", "--description", "D")
	require.Error(t, err)
	// Missing description entirely.
	_, err = runAgentCLI(t, srv, "propose", "--project", "p-1", "--title", "T")
	require.Error(t, err)
}

func TestAgentPropose_Non201IsAnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"not_found"}`))
	}))
	t.Cleanup(srv.Close)

	_, err := runAgentCLI(t, srv, "propose",
		"--project", "no-such", "--title", "T", "--description", "D")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "404")
}

// TestPRNumberFromDescription pins the Phase 32.10 pr-watch
// regex: the CLI extracts the first PR-like reference from the
// task description. Supported forms: "PR #N", "closes #N",
// "refs #N", "fixes #N", or a bare "#N" at the start of a line.
func TestPRNumberFromDescription(t *testing.T) {
	cases := []struct {
		name   string
		desc   string
		want   int
		wantOK bool
	}{
		{"pr-prefix", "PR #42 — implement feature", 42, true},
		{"closes-prefix", "closes #11 (Phase 32.7)", 11, true},
		{"refs-prefix", "refs #13 (Phase 32.9)", 13, true},
		{"fixes-prefix", "Fixes #7 — typo in README", 7, true},
		{"uppercase-PR", "PR #99 done", 99, true},
		{"first-match wins", "PR #5 wins over refs #6 later", 5, true},
		{"bare-hash-at-start", "#8 — small fix", 8, true},
		{"empty", "", 0, false},
		{"no-pr", "no PR reference here", 0, false},
		{"only-hash-not-at-start", "see issue #5 for context", 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := prNumberFromDescription(tc.desc)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tc.wantOK)
			}
			if got != tc.want {
				t.Fatalf("n = %d, want %d", got, tc.want)
			}
		})
	}
}
