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
//  5. A clean Bearer request (Authorization header, NO session cookie)
//     is exempt — agent/CLI flows carry no CSRF surface.
//  6. Cookie + Authorization together is NOT exempt: a hybrid request
//     falls into the Origin check (T174-CSRF-AUTHZ-SKIP-BYPASS
//     regression — the Authorization skip must not launder a request
//     that came with the victim's cookie).
//  7. GET with a foreign Origin passes — safe methods have no side
//     effects to forge.
//  8. Origin: null (sandboxed iframe) is rejected.
//  9. Origin with userinfo ("https://evil.com@host") is rejected —
//     browsers never send userinfo in Origin/Referer; fail closed.
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

// csrfPost sends a POST to /projects with the given Origin, Referer and
// Authorization headers ("" = omit) and returns the status code plus
// the error field of the JSON body. cookie may be "" — the request then
// goes out without a session cookie (clean-Bearer scenarios).
func csrfPost(t *testing.T, router http.Handler, cookie, origin, referer, authz string) (int, string) {
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
	if authz != "" {
		req.Header.Set("Authorization", authz)
	}
	if cookie != "" {
		req.AddCookie(&http.Cookie{Name: "orenda_session", Value: cookie})
	}
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
			status, errBody := csrfPost(t, r, cookie, tt.origin, tt.referer, "")
			assert.Equal(t, tt.wantStatus, status)
			if tt.wantErr != "" {
				assert.Equal(t, tt.wantErr, errBody)
			}
		})
	}
}

// TestCSRFOriginCheck_CleanBearerExempt pins the exemption for a CLEAN
// Bearer request: Authorization header present, NO session cookie.
// Agents/CLIs are not CSRF-exploitable — a cross-site page cannot set
// an Authorization header on a top-level navigation or non-preflighted
// request, so the middleware skips them entirely.
func TestCSRFOriginCheck_CleanBearerExempt(t *testing.T) {
	t.Parallel()
	router, _, jwt := csrfFixture(t)

	status, _ := csrfPost(t, router, "", "https://evil.com", "", "Bearer "+jwt)
	// 201 (created) proves the middleware let the clean Bearer through;
	// 403 would mean the exemption regressed.
	assert.Equal(t, http.StatusCreated, status)
}

// TestCSRFOriginCheck_CookieWithAuthzNotExempt is the T174-CSRF-AUTHZ-
// SKIP-BYPASS regression. A same-site attacker page (e.g.
// http://localhost:8080 — a different port is same-site for cookies,
// so the victim's Lax cookie rides along) can send a preflighted fetch
// carrying "Authorization: Bearer dummy" next to the cookie. With the
// old header-only skip, extractUserToken still authenticated the
// request by cookie while csrfOriginCheck skipped the Origin check on
// the bogus header — bypass. The fix: the exemption applies ONLY when
// no session cookie is present; cookie+Authorization together is a
// browser/hybrid request and must pass the Origin check.
func TestCSRFOriginCheck_CookieWithAuthzNotExempt(t *testing.T) {
	t.Parallel()
	router, cookie, _ := csrfFixture(t)

	status, errBody := csrfPost(t, router, cookie, "https://evil.com", "", "Bearer dummy")
	assert.Equal(t, http.StatusForbidden, status,
		"cookie + bogus Authorization must NOT bypass the Origin check")
	assert.Equal(t, "csrf_origin_mismatch", errBody)
}

// TestCSRFOriginCheck_UserinfoOriginRejected pins the sameOrigin
// fail-closed hardening: browsers never send userinfo components in
// Origin/Referer, so an origin like "https://evil.com@orenda.local:2137"
// (host under attacker control, victim host in userinfo) can only be a
// forged header — reject even though the userinfo string contains the
// app's host:port.
func TestCSRFOriginCheck_UserinfoOriginRejected(t *testing.T) {
	t.Parallel()
	router, cookie, _ := csrfFixture(t)

	status, errBody := csrfPost(t, router, cookie, "https://evil.com@orenda.local:2137", "", "")
	assert.Equal(t, http.StatusForbidden, status)
	assert.Equal(t, "csrf_origin_mismatch", errBody)
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
