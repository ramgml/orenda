package ws

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestExtractWSToken_CookieWins validates the auth precedence for /ws:
// the session cookie is the primary path (Phase 27.2), so the browser
// gets WS upgrade with no client-side token juggling. We don't fall
// through to ?token= when the cookie carries a value.
func TestExtractWSToken_CookieWins(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/api/v1/ws?token=query-token", nil)
	r.AddCookie(&http.Cookie{Name: "orenda_session", Value: "cookie-token"})
	got, ok := extractWSToken(r, "orenda_session")
	assert.True(t, ok, "should always return ok when cookie is present")
	assert.Equal(t, "cookie-token", got, "cookie takes precedence over ?token=")
}

// TestExtractWSToken_BearerHeader verifies that server-side clients
// without a cookie jar can authenticate via Authorization: Bearer.
func TestExtractWSToken_BearerHeader(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/api/v1/ws", nil)
	r.Header.Set("Authorization", "Bearer header-token")
	got, ok := extractWSToken(r, "orenda_session")
	assert.True(t, ok)
	assert.Equal(t, "header-token", got)
}

// TestExtractWSToken_QueryFallback keeps the legacy ?token= path alive
// for curl, integration tests, and external clients that don't speak
// cookies. Removal is non-breaking; this test pins the fallback.
func TestExtractWSToken_QueryFallback(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/api/v1/ws?token=legacy-token", nil)
	got, ok := extractWSToken(r, "orenda_session")
	assert.True(t, ok)
	assert.Equal(t, "legacy-token", got)
}

// TestExtractWSToken_Missing verifies the negative path: no cookie, no
// header, no query → 401 (the handler translates false into
// "missing token").
func TestExtractWSToken_Missing(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/api/v1/ws", nil)
	_, ok := extractWSToken(r, "orenda_session")
	assert.False(t, ok)
}

// TestExtractWSToken_EmptyCookieSkipped ensures an empty-valued cookie
// doesn't get returned as a valid token (otherwise an empty value would
// 401 with a misleading "invalid token" instead of the clearer "missing
// token" — same status, but a different code path inside the handler).
func TestExtractWSToken_EmptyCookieSkipped(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/api/v1/ws", nil)
	r.AddCookie(&http.Cookie{Name: "orenda_session", Value: ""})
	// Should fall through to query param absence and return ok=false.
	_, ok := extractWSToken(r, "orenda_session")
	assert.False(t, ok)
}

// TestExtractWSToken_CustomCookieName confirms the wiring respects a
// non-default cookie name (e.g. tests that inject their own session
// cookie without touching the global default).
func TestExtractWSToken_CustomCookieName(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/api/v1/ws", nil)
	r.AddCookie(&http.Cookie{Name: "orenda_dev", Value: "dev-token"})
	got, ok := extractWSToken(r, "orenda_dev")
	assert.True(t, ok)
	assert.Equal(t, "dev-token", got)

	// And the default-name cookie is ignored when we ask for a custom name.
	r2 := httptest.NewRequest(http.MethodGet, "/api/v1/ws", nil)
	r2.AddCookie(&http.Cookie{Name: "orenda_session", Value: "default-token"})
	r2.AddCookie(&http.Cookie{Name: "orenda_dev", Value: "dev-token"})
	got, ok = extractWSToken(r2, "orenda_dev")
	assert.True(t, ok)
	assert.Equal(t, "dev-token", got, "custom cookie name should be matched, not the default")
}
