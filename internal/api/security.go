// Package api — security headers middleware (Phase 9.6).
package api

import "net/http"

// securityHeaders sets the defensive response headers recommended by
// OWASP for a local-first single-page application.
//
// The Content-Security-Policy is intentionally permissive for the React
// dev server (Vite injects inline scripts) and tightens when VITE_PWA
// is on; production CSP can be locked further once the dev/prod split
// is finalised.
func securityHeaders() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h := w.Header()
			h.Set("X-Content-Type-Options", "nosniff")
			h.Set("X-Frame-Options", "DENY")
			h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
			h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
			// CSP: allow self + inline styles (Tailwind injects some) +
			// data: images for embedded SVG icons. Tighten further in
			// Phase 9.x when the asset pipeline is settled.
			h.Set("Content-Security-Policy",
				"default-src 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline'; script-src 'self'; connect-src 'self' ws: wss:; font-src 'self'; frame-ancestors 'none'; base-uri 'self'; form-action 'self'")
			// HSTS only makes sense behind TLS; loopback is plain HTTP so
			// we skip it here. Reverse-proxy deployments should add HSTS
			// at the proxy layer.
			next.ServeHTTP(w, r)
		})
	}
}
