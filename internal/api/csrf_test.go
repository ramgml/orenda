// T174: same-origin Origin/Referer CSRF check for cookie-authenticated
// mutations.
//
// Contracts pinned here:
//  1. A cookie-authenticated POST with a foreign Origin is rejected
//     (403 csrf_origin_mismatch) — the whole point of the middleware.
//  2. A cookie-authenticated POST with no Origin and no Referer still
//     passes — the lenient branch keeps curl/scripts and the existing
//     integration suite (which sends bare cookie mutations) working.
//  3. Same-origin Origin (including non-standard ports) passes.
//  4. A foreign Referer (without Origin) is rejected.
//  5. Bearer-authenticated requests are exempt — agent/CLI flows carry
//     no CSRF surface (a cross-site page cannot set the header).
//  6. GET with a foreign Origin passes — safe methods have no side
//     effects to forge.
//  7. Origin: null (sandboxed iframe) is rejected.
//
// The tests drive the full production router via the standard
// integration fixture, so the middleware wiring (right after
// RequireUser, inside the user group only) is part of what's verified.
package api_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func csrfFixture(t *testing.T) (router http.Handler, cookie, jwt string) {
	t.Helper()
	deps := integrationDeps(t)
	router = apiNewRouter(t, deps)

	cookie = loginAndCookie(t, router, "owner@x.com", "hunter2")

	// Bearer token for the same user via the login response payload —
	// loginHandler returns the raw JWT alongside the cookie.
	body, _ := json.Marshal(map[string]string{"email": "owner@x.com", "password": "hunter2"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(body))
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)
	var resp struct {
		Token string `json:"token"`
	}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &resp))
	require.NotEmpty(t, resp.Token)
	return router, cookie, resp.Token
}

// csrfPost sends a cookie-authenticated POST with the given Origin and
// Referer headers ("" = omit) and returns the status code plus the
// error field of the JSON body.
func csrfPost(t *testing.T, router http.Handler, cookie, origin, referer string) (int, string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/projects", bytes.NewReader([]byte(`{"name":"csrf-probe"}`)))
	// httptest defaults Host to "example.com"; a real browser request
	// to the loopback app carries the app's own host. Pin it so the
	// same-origin table rows compare against the host they name.
	req.Host = "orenda.local:2137"
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	if referer != "" {
		req.Header.Set("Referer", referer)
	}
	req.AddCookie(&http.Cookie{Name: "orenda_session", Value: cookie})
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	var body struct {
		Error string `json:"error"`
	}
	_ = json.Unmarshal(rr.Body.Bytes(), &body)
	return rr.Code, body.Error
}

func TestCSRFOriginCheck_CookieMutations(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		origin     string
		referer    string
		wantStatus int
		wantErr    string
	}{
		{
			name:       "foreign origin rejected",
			origin:     "https://evil.com",
			wantStatus: http.StatusForbidden,
			wantErr:    "csrf_origin_mismatch",
		},
		{
			name:       "foreign origin on foreign port rejected",
			origin:     "http://127.0.0.1:9999",
			wantStatus: http.StatusForbidden,
			wantErr:    "csrf_origin_mismatch",
		},
		{
			name:       "no origin and no referer passes (lenient, non-browser)",
			wantStatus: http.StatusCreated,
		},
		{
			name:       "same-origin origin passes (non-standard port)",
			origin:     "http://orenda.local:2137",
			wantStatus: http.StatusCreated,
		},
		{
			name:       "same-origin origin case-insensitive host passes",
			origin:     "http://ORENDA.LOCAL:2137",
			wantStatus: http.StatusCreated,
		},
		{
			name:       "https scheme same-origin origin passes",
			origin:     "https://orenda.local:2137",
			wantStatus: http.StatusCreated,
		},
		{
			name:       "foreign referer without origin rejected",
			referer:    "https://evil.com/login",
			wantStatus: http.StatusForbidden,
			wantErr:    "csrf_origin_mismatch",
		},
		{
			name:       "same-origin referer passes",
			referer:    "http://orenda.local:2137/projects",
			wantStatus: http.StatusCreated,
		},
		{
			name:       "origin null (sandboxed iframe) rejected",
			origin:     "null",
			wantStatus: http.StatusForbidden,
			wantErr:    "csrf_origin_mismatch",
		},
		{
			name:       "malformed origin rejected",
			origin:     "not a url",
			wantStatus: http.StatusForbidden,
			wantErr:    "csrf_origin_mismatch",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			// Fresh router+DB per subtest: the passing branches really
			// create a project, and independent fixtures keep the
			// assertions immune to subtest ordering.
			r, cookie, _ := csrfFixture(t)
			status, errBody := csrfPost(t, r, cookie, tt.origin, tt.referer)
			assert.Equal(t, tt.wantStatus, status)
			if tt.wantErr != "" {
				assert.Equal(t, tt.wantErr, errBody)
			}
		})
	}
}

func TestCSRFOriginCheck_BearerAuthExempt(t *testing.T) {
	t.Parallel()
	router, _, jwt := csrfFixture(t)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/projects", bytes.NewReader([]byte(`{"name":"bearer-probe"}`)))
	req.Header.Set("Authorization", "Bearer "+jwt)
	req.Header.Set("Origin", "https://evil.com")
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	// The foreign Origin must NOT block a Bearer client: agents/CLIs
	// are not CSRF-exploitable (a cross-site page cannot set the
	// Authorization header). 201 (created) proves the middleware let
	// the request through; 403 would mean the exemption regressed.
	assert.Equal(t, http.StatusCreated, rr.Code)
	assert.NotContains(t, rr.Body.String(), "csrf_origin_mismatch")
}

func TestCSRFOriginCheck_GETWithForeignOriginPasses(t *testing.T) {
	t.Parallel()
	router, cookie, _ := csrfFixture(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/projects", nil)
	req.Header.Set("Origin", "https://evil.com")
	req.AddCookie(&http.Cookie{Name: "orenda_session", Value: cookie})
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	// Safe methods pass regardless of Origin — no side effect to forge.
	assert.Equal(t, http.StatusOK, rr.Code)
}

func TestCSRFOriginCheck_LoginStillPublic(t *testing.T) {
	t.Parallel()
	router, _, _ := csrfFixture(t)

	// Login is mounted outside the user group: a foreign-Origin login
	// POST is processed normally (and fails on bad credentials), it is
	// NOT answered with csrf_origin_mismatch. This pins that the
	// middleware mount doesn't leak onto public endpoints.
	body := []byte(`{"email":"owner@x.com","password":"wrong"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(body))
	req.Header.Set("Origin", "https://evil.com")
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusUnauthorized, rr.Code)
	assert.NotContains(t, rr.Body.String(), "csrf_origin_mismatch")
}
