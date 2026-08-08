// Package api — Phase 10 webhook endpoints (VK callback).
//
// VK sends a POST to /api/v1/webhooks/vk with a JSON body. Two message
// types matter:
//
//   - confirmation: respond with the configured confirmation string.
//   - message_callback_event / message_event with payload {a, t, n, ts}:
//     an interactive button press; routed to bot.CallbackHandler.
package api

import (
	"encoding/json"
	"net/http"

	"github.com/ramgml/orenda/internal/bot"
)

// vkWebhookPayload is the envelope VK sends.
type vkWebhookPayload struct {
	Type   string `json:"type"`
	Group  int64  `json:"group_id"`
	Secret string `json:"secret,omitempty"`
	Object struct {
		PeerID  int64  `json:"peer_id"`
		Payload string `json:"payload"`
		EventID string `json:"event_id"`
	} `json:"object"`
}

// vkCallbackPayload is the inner payload of an interactive button press.
type vkCallbackPayload struct {
	Action string `json:"a"`
	TaskID string `json:"t"`
	Nonce  string `json:"n"`
	TS     int64  `json:"ts"`
}

// vkWebhookHandler processes VK callbacks.
//
// deps.VKConfirmation is the string VK expects back on a "confirmation"
// message (configure in the community's callback settings).
// deps.VKSecret is the shared secret VK includes; we verify it matches.
func vkWebhookHandler(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var p vkWebhookPayload
		if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_json"})
			return
		}

		if deps.VKSecret != "" && p.Secret != deps.VKSecret {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "bad_secret"})
			return
		}

		switch p.Type {
		case "confirmation":
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(deps.VKConfirmation))
			return
		case "message_event":
			var cb vkCallbackPayload
			if err := json.Unmarshal([]byte(p.Object.Payload), &cb); err != nil {
				writeJSON(w, http.StatusOK, map[string]string{"status": "ignored"})
				return
			}
			if deps.BotCallback != nil {
				_ = deps.BotCallback.Handle(r.Context(), bot.CallbackAction{
					Action:    cb.Action,
					TaskID:    cb.TaskID,
					Nonce:     cb.Nonce,
					BotUserID: int64ToString(p.Object.PeerID),
				})
			}
			writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
			return
		default:
			writeJSON(w, http.StatusOK, map[string]string{"status": "ignored"})
		}
	}
}

// int64ToString renders an int64 without strconv import.
func int64ToString(v int64) string {
	if v == 0 {
		return "0"
	}
	neg := v < 0
	if neg {
		v = -v
	}
	var buf [20]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
