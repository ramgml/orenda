package main

import (
	"context"
	"encoding/json"
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

// resolveAgentCtxForTest mirrors resolveAgentCtx but with
// test-injectable sources: opts carry flag values; env and the
// config files are read as usual (t.Setenv + t.TempDir control
// them). Run inside t.Chdir's working directory.
func resolveAgentCtxForTest(opts agentCLIOptions) (*agentCtx, error) {
	root := newAgentCmd()
	args := []string{}
	if opts.URL != "" {
		args = append(args, "--url", opts.URL)
	}
	if opts.Token != "" {
		args = append(args, "--token", opts.Token)
	}
	if err := root.ParseFlags(args); err != nil {
		panic(err)
	}
	return resolveAgentCtx(root)
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

// TestResolveAgentSettings_Chain walks the full per-field chain:
// flag > env (ORENDA_URL / ORENDA_AGENT_TOKEN) > ./.orenda/agent.yaml >
// ~/.config/orenda/agent.yaml. Local file with only token + global with
// only url must also resolve (fields are independent).
func TestResolveAgentSettings_Chain(t *testing.T) {
	tests := []struct {
		name    string
		flagURL string
		flagTok string
		envURL  string
		envTok  string
		local   *agentConfig
		global  *agentConfig
		wantURL string
		wantTok string
	}{
		{
			name:    "flag wins over everything",
			flagURL: "http://flag", flagTok: "tok-flag",
			envURL: "http://env", envTok: "tok-env",
			local:   &agentConfig{URL: "http://local", Token: "tok-local"},
			global:  &agentConfig{URL: "http://global", Token: "tok-global"},
			wantURL: "http://flag", wantTok: "tok-flag",
		},
		{
			name:   "env beats both files per field",
			envURL: "http://env", envTok: "tok-env",
			local:   &agentConfig{URL: "http://local", Token: "tok-local"},
			global:  &agentConfig{URL: "http://global", Token: "tok-global"},
			wantURL: "http://env", wantTok: "tok-env",
		},
		{
			name:    "local beats global",
			local:   &agentConfig{URL: "http://local", Token: "tok-local"},
			global:  &agentConfig{URL: "http://global", Token: "tok-global"},
			wantURL: "http://local", wantTok: "tok-local",
		},
		{
			name:    "mixed: local token + global url",
			local:   &agentConfig{Token: "tok-local"},
			global:  &agentConfig{URL: "http://global"},
			wantURL: "http://global", wantTok: "tok-local",
		},
		{
			name:    "global fills both when no local",
			global:  &agentConfig{URL: "http://global", Token: "tok-global"},
			wantURL: "http://global", wantTok: "tok-global",
		},
		{
			name:    "no sources at all errors on url first",
			wantURL: "", wantTok: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmp := t.TempDir()
			t.Chdir(tmp)
			t.Setenv("ORENDA_URL", tt.envURL)
			t.Setenv("ORENDA_AGENT_TOKEN", tt.envTok)
			xdg := t.TempDir()
			t.Setenv("XDG_CONFIG_HOME", xdg)
			writeCfg := func(rel string, cfg *agentConfig) {
				if cfg == nil {
					return
				}
				dir := filepath.Dir(rel)
				require.NoError(t, os.MkdirAll(dir, 0o755))
				raw, err := yaml.Marshal(cfg)
				require.NoError(t, err)
				require.NoError(t, os.WriteFile(rel, raw, 0o600))
			}
			writeCfg(localAgentConfigPath(), tt.local)
			writeCfg(filepath.Join(xdg, "orenda", "agent.yaml"), tt.global)

			root := newAgentCmd()
			args := []string{}
			if tt.flagURL != "" {
				args = append(args, "--url", tt.flagURL)
			}
			if tt.flagTok != "" {
				args = append(args, "--token", tt.flagTok)
			}
			require.NoError(t, root.ParseFlags(args))
			s, err := resolveAgentSettings(root, "orenda agent")
			if tt.wantURL == "" && tt.wantTok == "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), "--url")
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantURL, s.URL.Value)
			assert.Equal(t, tt.wantTok, s.Token.Value)
		})
	}
}

// TestResolveAgentSettings_LocalNotRequired: without ./.orenda the
// chain degrades to the old behavior (env > global).
func TestResolveAgentSettings_LocalNotRequired(t *testing.T) {
	xdg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdg)
	t.Setenv("ORENDA_URL", "http://from-env")
	t.Setenv("ORENDA_AGENT_TOKEN", "")
	raw, err := yaml.Marshal(agentConfig{URL: "http://global", Token: "tok-global"})
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Join(xdg, "orenda"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(xdg, "orenda", "agent.yaml"), raw, 0o600))
	t.Chdir(t.TempDir())

	root := newAgentCmd()
	require.NoError(t, root.ParseFlags(nil))
	s, err := resolveAgentSettings(root, "orenda agent")
	require.NoError(t, err)
	assert.Equal(t, "http://from-env", s.URL.Value)
	assert.Equal(t, "tok-global", s.Token.Value)
	assert.Equal(t, sourceEnv, s.URL.Source)
	assert.Equal(t, sourceGlobal, s.Token.Source)
}

// TestResolveAgentSettings_BrokenLocalYaml: a local config that
// exists but cannot be parsed is a hard error naming the path —
// never a silent fallback to the global file.
func TestResolveAgentSettings_BrokenLocalYaml(t *testing.T) {
	tmp := t.TempDir()
	t.Chdir(tmp)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("ORENDA_URL", "")
	t.Setenv("ORENDA_AGENT_TOKEN", "")
	require.NoError(t, os.MkdirAll(".orenda", 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(".orenda", "agent.yaml"), []byte("{unparsable: ["), 0o600))
	globalRaw, err := yaml.Marshal(agentConfig{URL: "http://global", Token: "tok-global"})
	require.NoError(t, err)
	xdg := os.Getenv("XDG_CONFIG_HOME")
	require.NoError(t, os.MkdirAll(filepath.Join(xdg, "orenda"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(xdg, "orenda", "agent.yaml"), globalRaw, 0o600))

	root := newAgentCmd()
	require.NoError(t, root.ParseFlags(nil))
	_, err = resolveAgentSettings(root, "orenda agent")
	require.Error(t, err)
	assert.Contains(t, err.Error(), filepath.Join(".orenda", "agent.yaml"),
		"error must name the broken local path")
}

// TestAgentConfigCmd_MasksToken: `orenda agent config` masks the
// token by default (human and json); --show-secret reveals it.
func TestAgentConfigCmd_MasksToken(t *testing.T) {
	t.Setenv("ORENDA_URL", "")
	t.Setenv("ORENDA_AGENT_TOKEN", "")
	tmp := t.TempDir()
	t.Chdir(tmp)
	raw, err := yaml.Marshal(agentConfig{URL: "http://from-local", Token: "tok-1234567890abcdef"})
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(".orenda", 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(".orenda", "agent.yaml"), raw, 0o600))

	run := func(extra ...string) string {
		t.Helper()
		root := newAgentCmd()
		var out strings.Builder
		root.SetOut(&out)
		args := append([]string{"config", "--json"}, extra...)
		root.SetArgs(args)
		require.NoError(t, root.Execute())
		return out.String()
	}

	masked := run()
	assert.Contains(t, masked, "tok-1234…")
	assert.NotContains(t, masked, "tok-1234567890abcdef")
	assert.Contains(t, masked, "\"source\":\"local\"")

	shown := run("--show-secret")
	assert.Contains(t, shown, "tok-1234567890abcdef")
}

func TestResolveAgentCtx_Missing(t *testing.T) {
	t.Setenv("ORENDA_URL", "")
	t.Setenv("ORENDA_AGENT_TOKEN", "")
	xdg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdg)
	_, err := resolveAgentCtxForTest(agentCLIOptions{})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	t.Logf("error: %s", err.Error())
	// The error must name the config file path so a fresh machine
	// knows where to put the credentials (the URL check fires first).
	wantPath := filepath.Join(xdg, "orenda", "agent.yaml")
	if !strings.Contains(err.Error(), "--url") {
		t.Errorf("error should mention --url, got: %s", err.Error())
	}
	if !strings.Contains(err.Error(), wantPath) {
		t.Errorf("error should contain the config path %q, got: %s", wantPath, err.Error())
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

// T83: `agent pages blocks get|put` — CLI shim over
// GET/PUT /api/v1/agent/pages/{slug}/blocks (the T81 REST surface).
// The server resolves the reference as a slug OR a W<N>, so the CLI
// must pass it through verbatim; the put→get round-trip mirrors the
// server-side TestAgentWikiBlocks_PutReplacesTree.

func TestAgentPagesBlocksGet_RefFormsAndJSONFlag(t *testing.T) {
	blockView := `{"format":"markdown","content_md":"Alpha content"}`
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(blockView))
	}))
	t.Cleanup(srv.Close)

	// Plain slug: pretty JSON.
	out, err := runAgentCLI(t, srv, "pages", "blocks", "get", "alpha")
	require.NoError(t, err)
	assert.Equal(t, "/api/v1/agent/pages/alpha/blocks", gotPath)
	assert.Contains(t, out, "Alpha content")

	// W<N> reference goes into the path as-is (server resolves it).
	out, err = runAgentCLI(t, srv, "pages", "blocks", "get", "W15")
	require.NoError(t, err)
	assert.Equal(t, "/api/v1/agent/pages/W15/blocks", gotPath)
	assert.Contains(t, out, "Alpha content")

	// --json: compact single-line output, like the sibling commands.
	out, err = runAgentCLI(t, srv, "pages", "blocks", "get", "alpha", "--json")
	require.NoError(t, err)
	assert.JSONEq(t, blockView, strings.TrimSpace(out))
}

func TestAgentPagesBlocksGet_UnknownSlugIsAnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"page_not_found"}`))
	}))
	t.Cleanup(srv.Close)

	_, err := runAgentCLI(t, srv, "pages", "blocks", "get", "no-such-page")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "404")
	assert.Contains(t, err.Error(), "page_not_found")
}

func TestAgentPagesBlocksPut_StdinRoundTrip(t *testing.T) {
	in := `[{"id":"c1","type":"paragraph","content":[{"type":"text","text":"Replaced"}]}]`
	var (
		gotMethod      string
		gotPath        string
		gotContentType string
		gotBlocks      []any
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		gotContentType = r.Header.Get("Content-Type")
		if r.Method == http.MethodPut {
			var body struct {
				Blocks []any `json:"blocks"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			gotBlocks = body.Blocks
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"id":"p-1","slug":"p1","content_format":"blocks"}`))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"format":"blocks","blocks":[{"id":"c1","type":"paragraph"}]}`))
	}))
	t.Cleanup(srv.Close)

	// put from stdin replaces the tree.
	root := newAgentCmd()
	var out strings.Builder
	root.SetOut(&out)
	root.SetIn(strings.NewReader(in))
	root.SetArgs([]string{"--url", srv.URL, "--token", "test-token",
		"pages", "blocks", "put", "p1"})
	require.NoError(t, root.Execute())
	assert.Equal(t, http.MethodPut, gotMethod)
	assert.Equal(t, "/api/v1/agent/pages/p1/blocks", gotPath)
	assert.Contains(t, gotContentType, "application/json")
	require.Len(t, gotBlocks, 1)
	assert.Equal(t, "c1", gotBlocks[0].(map[string]any)["id"])

	// The reverse get confirms the replacement took.
	getOut, err := runAgentCLI(t, srv, "pages", "blocks", "get", "p1")
	require.NoError(t, err)
	assert.Equal(t, http.MethodGet, gotMethod)
	assert.Contains(t, getOut, "blocks")
	assert.Contains(t, getOut, "c1")
}

func TestAgentPagesBlocksPut_FileFlag(t *testing.T) {
	bf := filepath.Join(t.TempDir(), "blocks.json")
	require.NoError(t, os.WriteFile(bf,
		[]byte(`[{"id":"b1","type":"heading","props":{"level":2}}]`), 0o600))
	var gotBlocks []any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Blocks []any `json:"blocks"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		gotBlocks = body.Blocks
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"p-1"}`))
	}))
	t.Cleanup(srv.Close)

	_, err := runAgentCLI(t, srv, "pages", "blocks", "put", "p1", "--file", bf)
	require.NoError(t, err)
	require.Len(t, gotBlocks, 1)
	assert.Equal(t, "b1", gotBlocks[0].(map[string]any)["id"])
}

func TestAgentPagesBlocksPut_InvalidBodyNotSent(t *testing.T) {
	hit := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hit = true
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	root := newAgentCmd()
	var out strings.Builder
	root.SetOut(&out)
	root.SetIn(strings.NewReader("not json at all"))
	root.SetArgs([]string{"--url", srv.URL, "--token", "test-token",
		"pages", "blocks", "put", "p1"})
	err := root.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "JSON array of blocks")
	assert.False(t, hit, "a non-JSON body must fail client-side, before the request")
}

func TestAgentPagesBlocksPut_Server404IsAnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"not_found"}`))
	}))
	t.Cleanup(srv.Close)

	root := newAgentCmd()
	var out strings.Builder
	root.SetOut(&out)
	root.SetIn(strings.NewReader(`[{"id":"c1","type":"paragraph"}]`))
	root.SetArgs([]string{"--url", srv.URL, "--token", "test-token",
		"pages", "blocks", "put", "missing-slug"})
	err := root.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "404")
	assert.Contains(t, err.Error(), "not_found")
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

// ---------------------------------------------------------------------------
// Task #42: CLI must percent-escape "#" in task IDs so it doesn't
// become a URL fragment separator.
// ---------------------------------------------------------------------------

func TestAgentContext_EscapesHashTaskID(t *testing.T) {
	var gotEscapedPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotEscapedPath = r.URL.EscapedPath()
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"task":{"id":"t-42","number":42,"title":"Test"}}`))
	}))
	t.Cleanup(srv.Close)

	_, err := runAgentCLI(t, srv, "context", "#42")
	require.NoError(t, err)
	assert.Equal(t, "/api/v1/agent/tasks/%2342/context", gotEscapedPath)
}

func TestAgentClaim_EscapesHashTaskID(t *testing.T) {
	var gotEscapedPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotEscapedPath = r.URL.EscapedPath()
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"t-42","status":"in_progress"}`))
	}))
	t.Cleanup(srv.Close)

	_, err := runAgentCLI(t, srv, "claim", "#42")
	require.NoError(t, err)
	assert.Equal(t, "/api/v1/agent/tasks/%2342/claim", gotEscapedPath)
}

func TestAgentSubmit_EscapesHashTaskID(t *testing.T) {
	var gotEscapedPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotEscapedPath = r.URL.EscapedPath()
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"t-42","status":"review"}`))
	}))
	t.Cleanup(srv.Close)

	_, err := runAgentCLI(t, srv, "submit", "#42")
	require.NoError(t, err)
	assert.Equal(t, "/api/v1/agent/tasks/%2342/submit", gotEscapedPath)
}

func TestAgentRelease_EscapesHashTaskID(t *testing.T) {
	var gotEscapedPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotEscapedPath = r.URL.EscapedPath()
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"t-42","status":"todo"}`))
	}))
	t.Cleanup(srv.Close)

	_, err := runAgentCLI(t, srv, "release", "#42")
	require.NoError(t, err)
	assert.Equal(t, "/api/v1/agent/tasks/%2342/release", gotEscapedPath)
}

func TestAgentUpdate_EscapesHashTaskID(t *testing.T) {
	var gotEscapedPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotEscapedPath = r.URL.EscapedPath()
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"t-42"}`))
	}))
	t.Cleanup(srv.Close)

	_, err := runAgentCLI(t, srv, "update", "#42", "--title", "new")
	require.NoError(t, err)
	assert.Equal(t, "/api/v1/agent/tasks/%2342", gotEscapedPath)
}

func TestAgentRetract_EscapesHashTaskID(t *testing.T) {
	var gotEscapedPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotEscapedPath = r.URL.EscapedPath()
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(srv.Close)

	_, err := runAgentCLI(t, srv, "retract", "#42")
	require.NoError(t, err)
	assert.Equal(t, "/api/v1/agent/tasks/%2342", gotEscapedPath)
}

func TestAgentComment_EscapesHashTaskID(t *testing.T) {
	var gotEscapedPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotEscapedPath = r.URL.EscapedPath()
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"c-1"}`))
	}))
	t.Cleanup(srv.Close)

	_, err := runAgentCLI(t, srv, "comment", "#42", "looks good")
	require.NoError(t, err)
	assert.Equal(t, "/api/v1/agent/tasks/%2342/comments", gotEscapedPath)
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
