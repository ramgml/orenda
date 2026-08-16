// Package ws — WebSocket client and HTTP upgrade handler.
package ws

import (
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/websocket"

	"github.com/ramgml/orenda/internal/auth"
)

// upgrader configures the HTTP→WebSocket upgrade. Origin is loopback-only,
// matching the CORS policy in internal/api/middleware.go.
var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin:     checkLoopbackOrigin,
}

func checkLoopbackOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		// No Origin: server-to-server or curl. Allow.
		return true
	}
	prefixes := []string{
		"http://localhost", "http://127.0.0.1", "http://[::1]",
		"https://localhost", "https://127.0.0.1", "https://[::1]",
	}
	for _, p := range prefixes {
		if len(origin) >= len(p) && origin[:len(p)] == p {
			return true
		}
	}
	return false
}

// Handler upgrades the request to a WebSocket connection, authenticates
// the user via the JWT carried in either the `orenda_session` cookie or
// the `?token=` query parameter, then runs read/write pumps.
//
// Phase 27.2: the previous design required `?token=` because the browser
// WebSocket API cannot set arbitrary request headers. Now that the UI
// authenticates with a same-origin cookie (Set-Cookie: orenda_session),
// the same cookie is sent automatically on the WS upgrade — no token
// juggling on the client. `?token=` is still accepted as a deprecated
// fallback so external integrations keep working.
//
// Phase 27.9: subscribe to every topic in AllTopics rather than just
// "tasks", so live updates reach the UI regardless of which surface
// (notifications, calendar, wiki, timers, …) emitted them. Single-owner
// deployment — no per-project filter needed.
//
// cookieName is the cookie carrying the session JWT (defaults to
// "orenda_session" when empty).
func Handler(hub Hub, signer *auth.Signer, cookieName string) http.Handler {
	if cookieName == "" {
		cookieName = "orenda_session"
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Phase 27.2: prefer the cookie (same-origin browser WS, no auth
		// state on the client). Fall back to ?token= for the legacy path
		// (curl, third-party integrations, and tests that don't have a
		// cookie jar handy).
		raw, ok := extractWSToken(r, cookieName)
		if !ok {
			http.Error(w, "missing token", http.StatusUnauthorized)
			return
		}
		claims, err := signer.Verify(raw)
		if err != nil {
			http.Error(w, "invalid token", http.StatusUnauthorized)
			return
		}
		userID := claims.Subject

		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			// upgrader already wrote an error response.
			return
		}

		// Phase 27.9: fan-out every topic so the UI receives live
		// updates from notifications, timers, calendar, wiki, etc.
		// Subscribe to each topic, merge the channels into one stream,
		// and release every subscription on disconnect.
		merged, cleanupAll := subscribeAll(hub, userID)
		go writePump(conn, merged)
		readPump(conn, cleanupAll)
	})
}

// subscribeAll opens one subscription per topic in AllTopics and merges
// them into a single channel. On disconnect the returned cleanup releases
// every subscription; partial cleanup would leak buffered events.
//
// We pick the fan-out shape (N subscriptions, 1 merged channel) rather
// than a single subscription to "all" because the hub models per-topic
// subscriber sets — emitting to every topic individually is the publish
// path already used by every service.
func subscribeAll(hub Hub, userID string) (<-chan Event, Unsubscribe) {
	merged := make(chan Event, 32)
	unsubs := make([]Unsubscribe, 0, len(AllTopics))
	for _, topic := range AllTopics {
		ch, unsub := hub.Subscribe(userID, topic)
		unsubs = append(unsubs, unsub)
		go func(c <-chan Event) {
			for ev := range c {
				merged <- ev
			}
		}(ch)
	}
	cleanup := func() {
		for _, u := range unsubs {
			u()
		}
	}
	return merged, cleanup
}

// extractWSToken pulls the JWT from the session cookie first, then the
// Authorization: Bearer header (useful for server-side clients that want
// to piggyback on the existing auth surface), then the legacy ?token=
// query parameter.
//
// Cookie wins because:
//  1. Browsers send it automatically on the WS upgrade, so the UI needs
//     no client-side token storage.
//  2. It's HttpOnly, so the JS layer can't accidentally exfiltrate it.
//  3. The Set-Cookie is already established by /auth/login on the same
//     origin — no extra round-trip.
func extractWSToken(r *http.Request, cookieName string) (string, bool) {
	if c, err := r.Cookie(cookieName); err == nil && c.Value != "" {
		return c.Value, true
	}
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
		tok := strings.TrimPrefix(h, "Bearer ")
		if tok != "" {
			return tok, true
		}
	}
	if q := r.URL.Query().Get("token"); q != "" {
		return q, true
	}
	return "", false
}

// writePump pumps events from the channel to the WebSocket connection.
//
// Includes a periodic ping to detect dead clients; browsers' WS proxies
// silently drop connections after extended idle time, so the ping keeps
// the connection warm and the client knows to reconnect.
func writePump(conn *websocket.Conn, events <-chan Event) {
	ticker := time.NewTicker(30 * time.Second)
	defer func() {
		ticker.Stop()
		_ = conn.Close()
	}()

	for {
		select {
		case ev, ok := <-events:
			if !ok {
				// Channel closed by unsub → peer left.
				return
			}
			_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := conn.WriteJSON(ev); err != nil {
				return
			}
		case <-ticker.C:
			_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

// readPump drains incoming frames. The WebSocket protocol requires the
// client to send data or pings; otherwise intermediaries close the
// connection. We don't expect any application messages, so any payload is
// silently dropped.
func readPump(conn *websocket.Conn, unsub Unsubscribe) {
	defer unsub()
	conn.SetReadLimit(1 << 16) // 64 KiB
	_ = conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	conn.SetPongHandler(func(string) error {
		_ = conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		return nil
	})
	for {
		if _, _, err := conn.ReadMessage(); err != nil {
			return
		}
	}
}
