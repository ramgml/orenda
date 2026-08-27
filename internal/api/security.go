// Package api — security headers middleware (Phase 9.6).
package api

import "net/http"

// securityHeaders sets the defensive response headers recommended by
// OWASP for a local-first single-page application.
//
// Content-Security-Policy history:
//
//   - Phase 28.10 tightened the policy to `style-src 'self'`,
//     assuming the production SPA ships all CSS under
//     /assets/index-*.css with zero inline style tags.
//   - T74 reverted that for `style-src` only: the SPA legitimately
//     injects `<style>` tags at runtime. The scroll-lock chain
//     (react-remove-scroll → react-remove-scroll-bar →
//     react-style-singleton) inserts overflow-compensation styles on
//     every overlay open — @mantine/core pulls the chain for
//     @blocknote/* editors and every Radix overlay (dialog, select,
//     popover, dropdown-menu) runs it too. React's own `style={{...}}`
//     props (progress bars, project colors, dnd-kit transforms) are
//     inline styles as well and were equally blocked. Neither nonce nor
//     hash enforcement is viable: the libraries do not propagate a
//     server-provided nonce, and their injected CSS mutates between
//     renders, so pinned hashes go stale immediately.
//
// `script-src` stays strict (`'self'`) — see the inline note below.
// Inline CSS remains a weaker exfiltration channel than inline script;
// accepting it here is the deliberate trade-off for a working editor
// and calendar UI.
func securityHeaders() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h := w.Header()
			h.Set("X-Content-Type-Options", "nosniff")
			h.Set("X-Frame-Options", "DENY")
			h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
			h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
			// CSP notes:
			//  - `font-src 'self' data: blob:` covers three cases:
			//    a) self-hosted .woff under /assets (default `'self'`)
			//    b) Workbox PWA cache re-serving them as blob URLs
			//    c) inline data URIs from build tooling.
			//    Allowing data:/blob: for fonts is safe — fonts can't
			//    inject script.
			//  - `style-src 'self' 'unsafe-inline'` — inline styles were
			//    re-allowed by T74: the react-style-singleton scroll-lock
			//    chain injects <style> tags on every overlay open
			//    (@mantine/@blocknote editors, Radix overlays), and
			//    React style={{...}} props are inline styles too.
			//    Nonce/hash is not viable — library-injected CSS changes
			//    per render and the libraries ignore our nonce. The risk
			//    is contained by keeping `script-src` strict ('self').
			//    The dev-server path uses a separate CSP via Vite's own
			//    dev-middleware (see web/vite.config.ts).
			h.Set("Content-Security-Policy",
				"default-src 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline'; script-src 'self'; connect-src 'self' ws: wss:; font-src 'self' data: blob:; frame-ancestors 'none'; base-uri 'self'; form-action 'self'")
			// HSTS only makes sense behind TLS; loopback is plain HTTP so
			// we skip it here. Reverse-proxy deployments should add HSTS
			// at the proxy layer.
			next.ServeHTTP(w, r)
		})
	}
}
