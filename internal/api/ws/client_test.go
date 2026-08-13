package ws

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

// TestSubscribeAll_FansOutAcrossTopics is the Phase 27.9 contract: a
// single subscription surface (subscribeAll) must receive events from
// every topic in AllTopics, not just "tasks". Pre-27.9 the WS handler
// only subscribed to "tasks", which silently dropped live updates for
// notifications, timers, calendar, wiki, comments, attachments, and
// agents events.
func TestSubscribeAll_FansOutAcrossTopics(t *testing.T) {
	hub := NewHub()

	merged, cleanup := subscribeAll(hub, "user-1")
	defer cleanup()

	// Publish one event to every topic in AllTopics and ensure each
	// reaches the merged channel.
	received := make(map[string]bool, len(AllTopics))
	var mu sync.Mutex
	done := make(chan struct{})

	go func() {
		timeout := time.After(2 * time.Second)
		for {
			select {
			case ev, ok := <-merged:
				if !ok {
					return
				}
				mu.Lock()
				received[ev.Topic] = true
				if len(received) == len(AllTopics) {
					mu.Unlock()
					close(done)
					return
				}
				mu.Unlock()
			case <-timeout:
				close(done)
				return
			}
		}
	}()

	for _, topic := range AllTopics {
		hub.Publish(context.Background(), Event{Topic: topic, Body: "x"})
	}

	<-done
	mu.Lock()
	defer mu.Unlock()
	for _, topic := range AllTopics {
		assert.True(t, received[topic], "topic %q did not fan out to merged channel", topic)
	}
}

// TestSubscribeAll_CleanupReleasesAllSubscriptions guards against a
// partial cleanup that would leak subscriptions — Phase 27.9 changes
// fan-out from 1 to N subscriptions, so a single missed unsub would
// silently retain a buffered channel after disconnect.
func TestSubscribeAll_CleanupReleasesAllSubscriptions(t *testing.T) {
	hub := NewHub()
	impl, ok := hub.(*channelHub)
	require.True(t, ok, "NewHub should return *channelHub; type assertion keeps this test honest if the implementation changes")

	_, cleanup := subscribeAll(hub, "user-1")

	// Pre-cleanup: every topic must have one subscriber.
	impl.mu.RLock()
	for _, topic := range AllTopics {
		_, present := impl.subs[topic]
		require.True(t, present, "expected subscriber on topic %q before cleanup", topic)
	}
	impl.mu.RUnlock()

	cleanup()

	impl.mu.RLock()
	defer impl.mu.RUnlock()
	require.Empty(t, impl.subs, "cleanup must release every per-topic subscription; saw leftover topics: %v", impl.subs)
}
