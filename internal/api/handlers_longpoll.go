// Package api — long-poll endpoint for agents without WebSocket support.
//
// The browser SPA uses the WebSocket hub (Phase 2.5); agents in CI
// pipelines prefer a one-shot POST /events/await that blocks until an
// event arrives or the timeout elapses. Both surfaces share the same
// in-process Hub.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"
)

// awaitRequest is the JSON body of POST /api/v1/events/await.
type awaitRequest struct {
	// Topic to subscribe to. Empty string means "any topic" (rare).
	Topic string `json:"topic"`

	// UserID to scope the wait (Phase 3 = single owner, the agent's
	// identity). When empty, all events for the topic are returned.
	UserID string `json:"user_id,omitempty"`

	// TimeoutSec is the maximum time to wait. Clamped to 60 seconds.
	TimeoutSec int `json:"timeout_s"`
}

// awaitResponse is the JSON returned on a hit.
type awaitResponse struct {
	Topic string `json:"topic"`
	Body  any    `json:"body"`
}

// awaitHandler subscribes to the WS hub for at most TimeoutSec and
// returns the first matching event as JSON.
//
//	204 No Content — timeout elapsed with no matching event.
//	200 OK        — event returned; body has {topic, body}.
//	400           — malformed body or invalid TimeoutSec.
//	401           — RequireUser middleware already gates this route.
func awaitHandler(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req awaitRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_json"})
			return
		}
		timeout := req.TimeoutSec
		if timeout <= 0 {
			timeout = 30
		}
		if timeout > 60 {
			timeout = 60
		}

		if deps.WSHub == nil {
			http.Error(w, "ws hub not configured", http.StatusServiceUnavailable)
			return
		}

		userID := req.UserID
		if id, ok := IdentityFrom(r.Context()); ok && userID == "" {
			userID = id.UserID
		}

		ctx, cancel := context.WithTimeout(r.Context(), time.Duration(timeout)*time.Second)
		defer cancel()

		events, unsub := deps.WSHub.Subscribe(userID, req.Topic)
		defer unsub()

		select {
		case <-ctx.Done():
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			http.Error(w, ctx.Err().Error(), http.StatusInternalServerError)
			return
		case ev, ok := <-events:
			if !ok {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			writeJSON(w, http.StatusOK, awaitResponse{Topic: ev.Topic, Body: ev.Body})
		}
	}
}
