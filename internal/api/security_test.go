package api_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ramgml/orenda/internal/api"
	"github.com/ramgml/orenda/internal/domain/user"
)

// noSuchUserRepo is a user.Repository that returns ErrNotFound for
// every lookup. The rate-limit tests use it so the login handler
// has something dereferable on deps.Users — otherwise it panics
// on the first request and the test fails for the wrong reason
// (panic instead of rate-limit assertion). The handler still
// responds 401 (invalid_credentials), which is all the rate-limit
// tests need to observe the burst window.
//
// We embed the user.Repository interface so future methods added
// to the interface compile here without immediate changes — the
// panic-on-Nil path is still gated behind the test's own setup.
type noSuchUserRepo struct{ user.Repository }

func (noSuchUserRepo) GetByEmail(context.Context, string) (*user.User, error) {
	return nil, user.ErrNotFound
}

// withRateLimitDeps returns a Dependencies wired with the bare
// minimum for the rate-limit tests: a nil-safe users repo. Other
// fields stay nil — the rate-limit tests only hit /api/v1/auth/login
// (and /api/v1/info / /healthz, which don't touch deps).
func withRateLimitDeps() *api.Dependencies {
	return &api.Dependencies{Users: noSuchUserRepo{}}
}

func TestSecurityHeaders_AreSet(t *testing.T) {
	router := api.NewRouter(withRateLimitDeps())
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
	router := api.NewRouter(withRateLimitDeps())
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	csp := rr.Header().Get("Content-Security-Policy")
	assert.Contains(t, csp, "font-src 'self' data: blob:",
		"CSP must include data:/blob: so cached/embedded fonts load")
}

// Phase 28.10: inline styles are no longer permitted. The SPA build
// pipeline (Vite + postcss + tailwind) emits all styles into
// /assets/index-*.css — there are zero `<style>` tags in the
// production index.html. Pinning the absence of 'unsafe-inline'
// stops a future contributor from quietly re-allowing the
// canonical CSS exfiltration channel (attribute-selector
// side-channels that read sensitive values via style.background).
//
// We also keep `style-src 'self'` rather than `style-src-attr 'none'`
// because some legacy browsers still need `style="..."` inline on
// individual DOM elements. Tightening further is a separate change.
func TestSecurityHeaders_StyleSrcNoUnsafeInline(t *testing.T) {
	router := api.NewRouter(withRateLimitDeps())
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	csp := rr.Header().Get("Content-Security-Policy")
	assert.Contains(t, csp, "style-src 'self'",
		"CSP must allow self-hosted stylesheets")
	assert.NotContains(t, strings.ToLower(csp), "'unsafe-inline'",
		"Phase 28.10: inline styles are forbidden — exfiltration vector")
}

// Phase 28.10: script-src stays locked to 'self'. The Vite SW
// registration is `<script src="/registerSW.js" ...>` (an external
// reference served by the SPA), which satisfies `'self'` without a
// nonce or hash. We pin this so a future contributor can't add
// inline event handlers (e.g. via a UI plugin) without first
// either inlining them with explicit hashes or wrapping via a
// nonce-aware render pass.
func TestSecurityHeaders_ScriptSrcSelfOnly(t *testing.T) {
	router := api.NewRouter(withRateLimitDeps())
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	csp := rr.Header().Get("Content-Security-Policy")
	// We accept either bare 'self' (production shape) or
	// 'self' followed by a nonce on dev/proxied paths. 'unsafe-inline'
	// remains the only forbidden token.
	assert.Contains(t, csp, "script-src 'self'",
		"script-src must keep 'self' as the base directive")
	assert.NotContains(t, strings.ToLower(csp), "'unsafe-inline'",
		"Phase 28.10: no inline scripts either")
}

func TestRateLimit_Anonymous429(t *testing.T) {
	router := api.NewRouter(withRateLimitDeps())

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
	router := api.NewRouter(withRateLimitDeps())
	for i := 0; i < 200; i++ {
		req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
		rr := httptest.NewRecorder()
		router.ServeHTTP(rr, req)
		require.Equal(t, http.StatusOK, rr.Code, "healthz must not be rate limited")
	}
}

// Phase 28.21: the login endpoint must NOT bypass the limiter. It is
// the single endpoint where brute force matters; the anon per-IP bucket
// (default burst 60) is the only thing standing between an attacker and
// unlimited password attempts. Previously login sat in SkipPaths "for
// E2E convenience" — E2E overrides the limits via ORENDA_RATELIMIT_*,
// so the skip was unnecessary. Pin the new behaviour.
//
// Uses a noSuchUserRepo so the login handler returns 401 (invalid
// credentials) instead of NPE'ing on users.GetByEmail — the test
// asserts the rate-limit contract, not the login correctness contract.
func TestRateLimit_LoginNotSkipped(t *testing.T) {
	router := api.NewRouter(withRateLimitDeps())

	var last *httptest.ResponseRecorder
	for i := 0; i < 100; i++ {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login",
			strings.NewReader(`{"email":"a@b.c","password":"x"}`))
		req.Header.Set("Content-Type", "application/json")
		req.RemoteAddr = "10.9.9.9:4444"
		rr := httptest.NewRecorder()
		router.ServeHTTP(rr, req)
		last = rr
	}
	require.Equal(t, http.StatusTooManyRequests, last.Code,
		"login must be rate limited after the anon burst is exhausted")
	assert.NotEmpty(t, last.Header().Get("Retry-After"))
}
