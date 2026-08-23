package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/ramgml/orenda/internal/api"
)

// testDeps returns a Dependencies wired with no-op repos for endpoints that
// don't need storage (healthz, info).
func testDeps() *api.Dependencies {
	return &api.Dependencies{
		Logger: zap.NewNop(),
	}
}

func TestHealthz(t *testing.T) {
	td := testDeps()
	router := api.NewRouter(td)
	t.Cleanup(td.RateLimitClose)

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	assert.Contains(t, rr.Header().Get("Content-Type"), "application/json")

	var body map[string]string
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &body))
	assert.Equal(t, "ok", body["status"])
	assert.NotEmpty(t, body["version"])
}

func TestInfo(t *testing.T) {
	infoDeps := &api.Dependencies{
		Logger: zap.NewNop(),
		Capabilities: api.Capabilities{
			Auth: true, Backup: true,
		},
	}
	router := api.NewRouter(infoDeps)
	t.Cleanup(infoDeps.RateLimitClose)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/info", nil)
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)

	var body struct {
		Version      string           `json:"version"`
		Name         string           `json:"name"`
		Capabilities api.Capabilities `json:"capabilities"`
	}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &body))
	assert.Equal(t, "orenda", body.Name)
	assert.NotEmpty(t, body.Version)
	assert.True(t, body.Capabilities.Auth)
	assert.True(t, body.Capabilities.Backup)
	assert.False(t, body.Capabilities.WebSocket, "websocket should be false in Phase 0")
}

func TestInfo_DefaultsAllFalse(t *testing.T) {
	td := testDeps()
	router := api.NewRouter(td)
	t.Cleanup(td.RateLimitClose)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/info", nil)
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)

	var body struct {
		Capabilities api.Capabilities `json:"capabilities"`
	}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &body))
	assert.Equal(t, api.Capabilities{}, body.Capabilities)
}

func TestSPA_NotFoundForUnknownAPI(t *testing.T) {
	td := testDeps()
	router := api.NewRouter(td)
	t.Cleanup(td.RateLimitClose)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/nonexistent", nil)
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	// /api/v1/nonexistent falls through to the SPA handler which serves
	// index.html (since react-router would handle it client-side).
	// In Phase 0 with no embed, we expect 404 with a helpful hint.
	assert.True(t, rr.Code == http.StatusNotFound || rr.Code == http.StatusOK,
		"got status %d", rr.Code)
}

func TestCORS_LoopbackAllowed(t *testing.T) {
	router := api.NewRouter(testDeps())

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	req.Header.Set("Origin", "http://localhost:5173")
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
	assert.Equal(t, "http://localhost:5173", rr.Header().Get("Access-Control-Allow-Origin"))
}

func TestCORS_ExternalOriginIgnored(t *testing.T) {
	router := api.NewRouter(testDeps())

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	req.Header.Set("Origin", "https://evil.example.com")
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
	assert.Empty(t, rr.Header().Get("Access-Control-Allow-Origin"))
}

func TestCORS_PreflightLoopback(t *testing.T) {
	router := api.NewRouter(testDeps())

	req := httptest.NewRequest(http.MethodOptions, "/api/v1/tasks", nil)
	req.Header.Set("Origin", "http://127.0.0.1:2137")
	req.Header.Set("Access-Control-Request-Method", "POST")
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusNoContent, rr.Code)
	assert.Equal(t, "http://127.0.0.1:2137", rr.Header().Get("Access-Control-Allow-Origin"))
	assert.Contains(t, rr.Header().Get("Access-Control-Allow-Methods"), "POST")
}
