// Package api — token-bucket rate limiter middleware (Phase 9.6).
//
// Two buckets:
//
//   - Per-IP for anonymous endpoints (login, /healthz excluded)
//   - Per-Identity for authenticated endpoints (user_id or token id)
//
// Bucket semantics: capacity = burst, refill rate = tokensPerSec. When the
// bucket is empty the request gets a 429 with a Retry-After header.
package api

import (
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"
)

// rateLimiter is a simple in-memory token bucket keyed by string.
//
// It's intentionally not Redis-backed — Phase 9 is single-process. The
// cleanup loop runs every minute and drops buckets idle for > 5 minutes
// so memory doesn't grow unboundedly.
type rateLimiter struct {
	mu       sync.Mutex
	buckets  map[string]*bucket
	capacity float64
	refill   float64 // tokens per second
	done     chan struct{}
}

type bucket struct {
	tokens float64
	last   time.Time
}

// newRateLimiter returns a rate limiter. capacity is the burst size;
// refillPerSec is the steady-state rate.
func newRateLimiter(capacity int, refillPerSec float64) *rateLimiter {
	rl := &rateLimiter{
		buckets:  make(map[string]*bucket),
		capacity: float64(capacity),
		refill:   refillPerSec,
		done:     make(chan struct{}),
	}
	go rl.cleanupLoop()
	return rl
}

// Close stops the background cleanup goroutine. Safe to call more than once.
func (rl *rateLimiter) Close() {
	select {
	case <-rl.done:
		// already closed
	default:
		close(rl.done)
	}
}

// allow returns true if a token is available for key.
func (rl *rateLimiter) allow(key string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	now := time.Now()
	b, ok := rl.buckets[key]
	if !ok {
		b = &bucket{tokens: rl.capacity, last: now}
		rl.buckets[key] = b
	}
	// Refill.
	elapsed := now.Sub(b.last).Seconds()
	b.tokens += elapsed * rl.refill
	if b.tokens > rl.capacity {
		b.tokens = rl.capacity
	}
	b.last = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// retryAfterSeconds returns how long until the bucket would have a token.
func (rl *rateLimiter) retryAfterSeconds(key string) int {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	b, ok := rl.buckets[key]
	if !ok || b.tokens >= 1 {
		return 0
	}
	need := 1 - b.tokens
	secs := need / rl.refill
	if secs < 1 {
		return 1
	}
	return int(secs + 0.5)
}

func (rl *rateLimiter) cleanupLoop() {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-rl.done:
			return
		case <-t.C:
			rl.mu.Lock()
			cutoff := time.Now().Add(-5 * time.Minute)
			for k, b := range rl.buckets {
				if b.last.Before(cutoff) {
					delete(rl.buckets, k)
				}
			}
			rl.mu.Unlock()
		}
	}
}

// rateLimitOptions configures the rate limit middleware.
type rateLimitOptions struct {
	// Anon burst + rate (per IP).
	AnonBurst  int
	AnonPerSec float64

	// Auth burst + rate (per user / token).
	AuthBurst  int
	AuthPerSec float64

	// Paths excluded from limiting (healthz, ws upgrade).
	SkipPaths map[string]bool
}

// rateLimitResult bundles the middleware with a Close function that
// stops the background cleanup goroutines. Callers SHOULD call Close
// (typically via t.Cleanup) to avoid goroutine leaks.
type rateLimitResult struct {
	middleware func(http.Handler) http.Handler
	close      func()
}

// rateLimit returns the rate-limit middleware and a cleanup function.
//
// Order: this middleware runs AFTER auth middleware so IdentityFrom is
// available when present.
func rateLimit(opts rateLimitOptions) rateLimitResult {
	anon := newRateLimiter(opts.AnonBurst, opts.AnonPerSec)
	auth := newRateLimiter(opts.AuthBurst, opts.AuthPerSec)

	mw := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if opts.SkipPaths[r.URL.Path] {
				next.ServeHTTP(w, r)
				return
			}

			if id, ok := IdentityFrom(r.Context()); ok && (id.UserID != "" || id.AgentID != "") {
				key := id.UserID
				if key == "" {
					key = id.AgentID
				}
				if !auth.allow(key) {
					w.Header().Set("Retry-After", strconv.Itoa(auth.retryAfterSeconds(key)))
					writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "rate_limited"})
					return
				}
				next.ServeHTTP(w, r)
				return
			}

			// Anonymous: per-IP.
			host, _, err := net.SplitHostPort(r.RemoteAddr)
			if err != nil {
				host = r.RemoteAddr
			}
			if !anon.allow(host) {
				w.Header().Set("Retry-After", strconv.Itoa(anon.retryAfterSeconds(host)))
				writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "rate_limited"})
				return
			}
			next.ServeHTTP(w, r)
		})
	}

	return rateLimitResult{
		middleware: mw,
		close: func() {
			anon.Close()
			auth.Close()
		},
	}
}
