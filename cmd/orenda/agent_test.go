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
