// Package api — security headers middleware (Phase 9.6).
package api

import "net/http"

// securityHeaders sets the defensive response headers recommended by
// OWASP for a local-first single-page application.
//
// Phase 28.10 (polish): Content-Security-Policy tightened.
// Pre-28.10 we kept `style-src 'unsafe-inline'` because Tailwind's
// dev mode injects inline style blocks for HMR. With the SPA build
// pipeline now consolidated (Vite + postcss + tailwind), the
// production index.html carries zero inline `<style>` tags — all
// styles live under /assets/index-*.css, served via `'self'`. We
// drop `'unsafe-inline'` from `style-src`, which is the canonical
// CSP pollution vector (inline styles can leak data via
// attribute selectors and exfiltration via `style.background`).
//
// The `script-src 'self'` line is already minimal — Vite emits the
// service-worker registration as `<script src="/registerSW.js" ...>`
// (an external reference, not inline), so no nonce/hash is needed.
// The Workbox runtime lives under /assets/workbox-*.js, also served
// via `'self'`.
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
			//  - `style-src 'self'` — `'unsafe-inline'` removed in 28.10.
			//    If a future feature needs inline styles, prefer
			//    `'sha256-...'` per-block over `'unsafe-inline'` so the
			//    policy stays locked. The dev-server path uses a
			//    separate CSP via Vite's own dev-middleware (see
			//    web/vite.config.ts), so dev still works.
			h.Set("Content-Security-Policy",
				"default-src 'self'; img-src 'self' data:; style-src 'self'; script-src 'self'; connect-src 'self' ws: wss:; font-src 'self' data: blob:; frame-ancestors 'none'; base-uri 'self'; form-action 'self'")
			// HSTS only makes sense behind TLS; loopback is plain HTTP so
			// we skip it here. Reverse-proxy deployments should add HSTS
			// at the proxy layer.
			next.ServeHTTP(w, r)
		})
	}
}
