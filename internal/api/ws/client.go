// Package ws — WebSocket client and HTTP upgrade handler.
package ws

import (
	"net/http"
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
// the user via the JWT in the ?token= query parameter (the browser WS API
// can't set headers), then runs read/write pumps.
//
// writeTimeout governs how often we send heartbeats and the maximum time we
// wait for a slow consumer.
func Handler(hub Hub, signer *auth.Signer) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Auth: token in ?token= query param (browser WebSocket limitation).
		raw := r.URL.Query().Get("token")
		if raw == "" {
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

		// Subscribe to the default task-events topic. Phase 2 only has one
		// topic; Phase 3 will add per-project subscriptions.
		events, unsub := hub.Subscribe(userID, "tasks")
		go writePump(conn, events)
		readPump(conn, unsub)
	})
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
