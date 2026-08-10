package api_test

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ramgml/orenda/internal/api"
)

func TestSecurityHeaders_AreSet(t *testing.T) {
	router := api.NewRouter(api.Dependencies{})
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	assert.Equal(t, "nosniff", rr.Header().Get("X-Content-Type-Options"))
	assert.Equal(t, "DENY", rr.Header().Get("X-Frame-Options"))
	assert.NotEmpty(t, rr.Header().Get("Content-Security-Policy"))
	assert.Contains(t, rr.Header().Get("Content-Security-Policy"), "default-src 'self'")
}

// Regression: Workbox PWA serves cached woff fonts via blob: URLs.
// The original policy `font-src 'self'` blocked those, surfacing as
// `Loading the font <URL> violates the following Content Security
// Policy directive: "font-src 'self'"` in DevTools. We extend with
// data: and blob: which is safe — fonts can't inject script.
func TestSecurityHeaders_FontSrcAllowsBlobAndData(t *testing.T) {
	router := api.NewRouter(api.Dependencies{})
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	csp := rr.Header().Get("Content-Security-Policy")
	assert.Contains(t, csp, "font-src 'self' data: blob:",
		"CSP must include data:/blob: so cached/embedded fonts load")
}

func TestRateLimit_Anonymous429(t *testing.T) {
	router := api.NewRouter(api.Dependencies{})

	// Flood /api/v1/info (no auth, low anon burst).
	// The default burst is 60 with 20/sec refill; send 100 quickly.
	var last *httptest.ResponseRecorder
	for i := 0; i < 100; i++ {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/info", nil)
		req.RemoteAddr = "10.0.0.1:12345"
		rr := httptest.NewRecorder()
		router.ServeHTTP(rr, req)
		last = rr
	}
	require.Equal(t, http.StatusTooManyRequests, last.Code, "expected 429 after burst exhausted")
	retry := last.Header().Get("Retry-After")
	assert.NotEmpty(t, retry)
	_, err := strconv.Atoi(retry)
	assert.NoError(t, err)
}

func TestRateLimit_HealthzSkipped(t *testing.T) {
	router := api.NewRouter(api.Dependencies{})
	for i := 0; i < 200; i++ {
		req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
		rr := httptest.NewRecorder()
		router.ServeHTTP(rr, req)
		require.Equal(t, http.StatusOK, rr.Code, "healthz must not be rate limited")
	}
}
