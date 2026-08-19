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

// recovererMiddleware is a thin alias to chi's middleware.Recoverer.
var recovererMiddleware = middleware.Recoverer

// requestLogger emits a structured zap entry per request.
//
// We avoid chi's middleware.Logger because it writes plain-text logs that
// conflict with the project's structured-only logging rule (AGENTS.md).
//
// Phase 24: every request also updates the in-process stats counters
// (liveStats). Requests taking > 500ms emit a separate zap.Warn with
// the same fields so a slow path is visible without grepping the
// info stream.
func requestLogger(logger *zap.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
			next.ServeHTTP(ww, r)
			dur := time.Since(start)
			status := ww.Status()
			fields := []zap.Field{
				zap.String("method", r.Method),
				zap.String("path", r.URL.Path),
				zap.Int("status", status),
				zap.Int("bytes", ww.BytesWritten()),
				zap.String("remote", r.RemoteAddr),
				zap.String("request_id", middleware.GetReqID(r.Context())),
				zap.Duration("duration", dur),
			}
			if dur > slowRequestThreshold {
				logger.Warn("http.slow", fields...)
				liveStats.recordStats(status, true)
			} else {
				logger.Info("http", fields...)
				liveStats.recordStats(status, false)
			}
		})
	}
}

// slowRequestThreshold defines what counts as "slow". 500ms is a
// rule-of-thumb for an interactive API; everything under that is
// acceptable for a single-owner local install.
const slowRequestThreshold = 500 * time.Millisecond

// ResetLiveStats zeroes the in-process counters. Tests use this to
// establish a clean baseline before asserting on /api/v1/stats.
func ResetLiveStats() {
	liveStats.totalReq.Store(0)
	for i := range liveStats.byStatus {
		liveStats.byStatus[i].Store(0)
	}
	liveStats.slowCount.Store(0)
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
