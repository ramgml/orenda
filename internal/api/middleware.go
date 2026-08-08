// Package api — middleware (request logging, CORS, recovery, request ID, real IP).
package api

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5/middleware"
	"go.uber.org/zap"
)

// requestIDMiddleware is a thin alias to chi's middleware.RequestID.
var requestIDMiddleware = middleware.RequestID

// realIPMiddleware is a thin alias to chi's middleware.RealIP.
var realIPMiddleware = middleware.RealIP

// recovererMiddleware is a thin alias to chi's middleware.Recoverer.
var recovererMiddleware = middleware.Recoverer

// requestLogger emits a structured zap entry per request.
//
// We avoid chi's middleware.Logger because it writes plain-text logs that
// conflict with the project's structured-only logging rule (AGENTS.md).
func requestLogger(logger *zap.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
			next.ServeHTTP(ww, r)
			logger.Info("http",
				zap.String("method", r.Method),
				zap.String("path", r.URL.Path),
				zap.Int("status", ww.Status()),
				zap.Int("bytes", ww.BytesWritten()),
				zap.String("remote", r.RemoteAddr),
				zap.String("request_id", middleware.GetReqID(r.Context())),
				zap.Duration("duration", time.Since(start)),
			)
		})
	}
}

// corsLoopback configures CORS for the local-first use case.
//
// Loopback origins are always allowed. External origins receive no
// Access-Control-Allow-Origin header (browser will block).
//
// Phase 1+ may widen this to a configurable allow-list once a reverse-proxy
// story is defined.
func corsLoopback() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
			if isLoopbackOrigin(origin) {
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Set("Vary", "Origin")
				w.Header().Set("Access-Control-Allow-Credentials", "true")
				w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
				w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Request-ID")
			}
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// isLoopbackOrigin reports whether origin is a localhost or 127.0.0.1 URL.
//
// We accept any port; the production deployment runs on a single port and
// the loopback interface is the security boundary.
func isLoopbackOrigin(origin string) bool {
	if origin == "" {
		return false
	}
	prefixes := []string{
		"http://localhost",
		"http://127.0.0.1",
		"http://[::1]",
		"https://localhost",
		"https://127.0.0.1",
		"https://[::1]",
	}
	for _, p := range prefixes {
		if len(origin) >= len(p) && origin[:len(p)] == p {
			return true
		}
	}
	return false
}
