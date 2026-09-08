// Package api — T174: same-origin CSRF check for cookie-authenticated
// mutations.
//
// The session cookie is SameSite=Lax, which already blocks cross-site
// POSTs driven by classic form auto-submission. Lax still lets the
// cookie ride along on top-level navigations (GET), so the residual
// risk for mutations is narrower — but a Lax cookie is not a CSRF
// defence by itself: it is a browser behaviour, and the top-level
// navigation channel plus subdomain / parked-domain cookie-scoping
// issues (attacker page on *.neighbour.tld posting to our origin) stay
// inside the grey zone. The security review of PR #183 flagged exactly
// this; T174 adds an explicit Origin/Referer check as defence in depth.
package api

import (
	"net/http"
	"net/url"
	"strings"
)

// csrfOriginCheck returns a middleware enforcing a same-origin check on
// state-changing requests (everything except GET/HEAD/OPTIONS).
//
// Why it exists (T174, security review of PR #183): cookie
// authentication alone cannot tell which site fired a request — the
// browser attaches the session cookie regardless of the initiating
// page. SameSite=Lax removes the classic cross-site form-POST vector,
// but leaves a grey zone: cookies still ride on top-level navigations,
// and sibling-subdomain / parked-domain pages can scope cookies into a
// shared registrable domain. An explicit Origin/Referer comparison
// closes those paths for cookie-authenticated mutations.
//
// Rules, in order:
//
//   - Safe methods (GET/HEAD/OPTIONS) pass — they must stay side-effect
//     free by contract anyway.
//   - Requests carrying an Authorization header pass. A cross-site
//     attacker cannot set arbitrary request headers (no CORS-preflight
//     from a page it controls, no custom headers on top-level
//     navigations), so Bearer-authenticated clients (agents, CLIs) are
//     not CSRF-exploitable; skipping them keeps those flows untouched.
//   - If Origin or Referer is present, its URL host (host:port) is
//     compared with r.Host, case-insensitively; both http and https
//     schemes are accepted. A mismatch is a cross-site request → 403
//     {"error":"csrf_origin_mismatch"}.
//   - An Origin of "null" is rejected — it is what sandboxed iframes
//     send, and no legitimate same-origin request from the SPA
//     produces it.
//   - If NEITHER header is present, the request passes. Browsers send
//     Origin on every POST (and CORS-safelisted cross-origin
//     requests always carry it); its absence therefore identifies a
//     non-browser client (curl, scripts, integration tests). The
//     check is deliberately lenient here so plain curl workflows and
//     the existing Go integration suite (which performs cookie
//     mutations without Origin headers) keep working.
func csrfOriginCheck() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.Method {
			case http.MethodGet, http.MethodHead, http.MethodOptions:
				next.ServeHTTP(w, r)
				return
			}
			// Bearer clients (agents/CLI) are immune: a cross-site page
			// cannot plant an arbitrary Authorization header. Skip them.
			if r.Header.Get("Authorization") != "" {
				next.ServeHTTP(w, r)
				return
			}
			origin := r.Header.Get("Origin")
			if origin == "" {
				origin = r.Header.Get("Referer")
			}
			if origin == "" {
				// No Origin and no Referer: not a browser → CSRF is not
				// possible. Lenient on purpose (see doc comment).
				next.ServeHTTP(w, r)
				return
			}
			if !sameOrigin(origin, r.Host) {
				writeJSON(w, http.StatusForbidden, map[string]string{"error": "csrf_origin_mismatch"})
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// sameOrigin reports whether raw (an Origin or Referer header value)
// parses to the given host (r.Host form: "host" or "host:port"),
// case-insensitively. Both http and https schemes are accepted — the
// loopback install runs plain HTTP while proxied installs terminate
// TLS, and the host comparison is the security-relevant part.
//
// "null" (sandboxed iframe) never matches.
func sameOrigin(raw, host string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return false
	}
	return strings.EqualFold(u.Host, host)
}
