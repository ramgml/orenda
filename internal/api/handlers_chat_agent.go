// Package api — T9 dashboard agent chat endpoints (agent side).
//
// Mounted under RequireAgent (bearer), mirroring the tutor pattern:
//   - GET  /api/v1/agent/chat/pending — queue of pending user
//     questions (message + user_id + user_display_name + thread_id).
//   - POST /api/v1/agent/chat/{message_id}/reply — answer one
//     pending question (201 chat.Message; unknown id 404 / not
//     pending 409 / empty body 400).
//
// Both fan the new message out on the WS topic "dashboard-chat"
// (nil-safe) so the user's panel updates live.
package api

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/ramgml/orenda/internal/service/chatdialog"
)

// chatAgentReplyBody is the wire shape of POST /agent/chat/{message_id}/reply.
type chatAgentReplyBody struct {
	BodyMD string `json:"body_md"`
}

// chatAgentPendingHandler lists the pending dashboard chat questions
// for the agent. Open to any valid agent token: the single-owner
// install has one dashboard agent; per-agent scoping is a future
// concern.
func chatAgentPendingHandler(deps *Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps.ChatDialog == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "chat_not_wired"})
			return
		}
		pending, err := deps.ChatDialog.Pending(r.Context())
		if err != nil {
			writeChatDialogError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"pending": pending})
	}
}

// chatAgentReplyHandler records the agent's answer to one pending
// dashboard chat question. The thread + owner are derived from the
// question row, so the reply body carries only the text.
func chatAgentReplyHandler(deps *Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps.ChatDialog == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "chat_not_wired"})
			return
		}
		var body chatAgentReplyBody
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_json"})
			return
		}
		mid := chi.URLParam(r, "message_id")
		if mid == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing_message_id"})
			return
		}
		m, err := deps.ChatDialog.Reply(r.Context(), mid, body.BodyMD)
		if err != nil {
			writeChatDialogError(w, err)
			return
		}
		publishChat(r.Context(), deps, m)
		writeJSON(w, http.StatusCreated, m)
	}
}

// writeChatDialogError maps chatdialog service sentinels onto HTTP
// statuses: 404 / 400 / 409, everything else through writeError.
func writeChatDialogError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, chatdialog.ErrNotFound):
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not_found"})
	case errors.Is(err, chatdialog.ErrInvalidInput):
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_input"})
	case errors.Is(err, chatdialog.ErrConflict):
		writeJSON(w, http.StatusConflict, map[string]string{"error": "conflict"})
	default:
		writeError(w, err)
	}
}
