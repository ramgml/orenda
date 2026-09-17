package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/ramgml/orenda/internal/api/ws"
	"github.com/ramgml/orenda/internal/domain/chat"
	studyservice "github.com/ramgml/orenda/internal/service/study"
)

// chatPostBody is the wire shape for POST /api/v1/dashboard/chat.
//
// T9: messages starting with "/" are commands, anything else is a
// plain question for the dashboard agent. Commands:
//   - "/plan day"     → triggers study-proposal pipeline. Returns the
//     proposal id in result_ref so the UI can link back.
//   - "/help"         → static text.
//
// Plain text (no "/") is persisted as the user's message and left
// pending — the dashboard agent answers it via /agent/chat.
type chatPostBody struct {
	ThreadID string `json:"thread_id"`
	Message  string `json:"message"`
}

// chatPostResponse is the wire shape we return. agent_message is
// null and pending=true when the message is plain text — the
// dashboard agent answers it later via /agent/chat.
type chatPostResponse struct {
	UserMessage  *chat.Message `json:"user_message"`
	AgentMessage *chat.Message `json:"agent_message"`
	ResultRef    string        `json:"result_ref"`
	Pending      bool          `json:"pending"`
}

// postDashboardChatHandler — Phase 32.11 chat endpoint, extended
// by T9 with per-user threads and the pending-question flow.
//
// Command messages:
//   - /plan day  → server calls StudyService.Propose with a
//     generic daily-plan payload. The /plan result lands in the
//     existing study-proposals tray (Phase 31.6) so the user can
//     accept/dismiss via the existing UI.
//   - /help      → static text.
//
// Plain text: persisted as the user's message and left pending
// (agent_message=null, pending=true); the dashboard agent
// answers via GET /agent/chat/pending + POST
// /agent/chat/{id}/reply.
//
// The endpoint persists messages via ChatMessages so the
// Dashboard can replay history on page load (ListByUserThread,
// scoped to the signed-in user). Live updates fan out over the
// WS topics "chat" and "dashboard-chat".
//
// UI: web/src/features/today/DashboardChatPanel.tsx (T9).
func postDashboardChatHandler(deps *Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps.ChatMessages == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "chat_not_wired"})
			return
		}
		var body chatPostBody
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_json"})
			return
		}
		if strings.TrimSpace(body.Message) == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "empty_message"})
			return
		}
		userID := userIDFromCtx(r)
		if userID == "" {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
			return
		}
		if body.ThreadID == "" {
			body.ThreadID = "default"
		}

		// T9: record thread ownership so only this user can replay
		// it later. A failed upsert must not eat the message — but
		// it IS a data bug, so surface it.
		if deps.ChatThreads != nil {
			if err := deps.ChatThreads.Upsert(r.Context(), userID, body.ThreadID); err != nil {
				writeError(w, err)
				return
			}
		}

		user := &chat.Message{
			UserID:     userID,
			ThreadID:   body.ThreadID,
			SenderType: chat.SenderUser,
			BodyMD:     body.Message,
			Command:    extractCommand(body.Message),
			CreatedAt:  time.Now().UTC(),
		}
		if err := deps.ChatMessages.Create(r.Context(), user); err != nil {
			writeError(w, err)
			return
		}
		publishChat(r.Context(), deps, user)

		// T9: plain text has no command to dispatch — the message
		// stays pending and the dashboard agent answers later via
		// GET /agent/chat/pending + POST /agent/chat/{id}/reply.
		// Commands (leading "/") dispatch immediately as before.
		if user.Command == "" {
			writeJSON(w, http.StatusCreated, chatPostResponse{
				UserMessage: user,
				Pending:     true,
			})
			return
		}

		agent, resultRef, err := dispatchChatCommand(r.Context(), deps, body)
		if err != nil {
			writeError(w, err)
			return
		}
		agent.UserID = userID
		if err := deps.ChatMessages.Create(r.Context(), agent); err != nil {
			writeError(w, err)
			return
		}
		publishChat(r.Context(), deps, agent)
		writeJSON(w, http.StatusCreated, chatPostResponse{
			UserMessage:  user,
			AgentMessage: agent,
			ResultRef:    resultRef,
		})
	}
}

// getDashboardChatHandler — list messages for a thread (replay on
// page load). Capped at 50; pass ?limit=N for larger windows.
func getDashboardChatHandler(deps *Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps.ChatMessages == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "chat_not_wired"})
			return
		}
		userID := userIDFromCtx(r)
		if userID == "" {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
			return
		}
		thread := chi.URLParam(r, "thread")
		if thread == "" {
			thread = "default"
		}
		// T9: only the thread owner replays it. A thread another
		// user opened (or one that does not exist) is an empty
		// history — 200 with an empty list, not 404, so the UI
		// just renders an empty pane.
		if deps.ChatThreads != nil {
			owned, err := deps.ChatThreads.Owned(r.Context(), userID, thread)
			if err != nil {
				writeError(w, err)
				return
			}
			if !owned {
				writeJSON(w, http.StatusOK, map[string]any{"messages": []any{}})
				return
			}
		}
		msgs, err := deps.ChatMessages.ListByUserThread(r.Context(), userID, thread, 50)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"messages": msgs})
	}
}

// dispatchChatCommand routes the user message to the right backend
// side-effect and returns the agent reply + result_ref.
//
// T9 command set (plain text never reaches here — the handler
// returns early when Command is empty; the default arm only sees
// unknown "/commands"):
//   - "/plan day"  → StudyService.Propose with a generic daily-plan
//     payload. The actor id is the literal "chat";
//     study_proposals.created_by_agent has a FK to
//     agents(id), satisfied by migration 050 (seeds the
//     agents row id='chat'). The /plan result lands in
//     the study-proposals tray (Phase 31.6); result_ref
//     is the proposal id.
//   - "/help"      → static help.
//   - unknown "/cmd" → acknowledgement reply.
func dispatchChatCommand(ctx context.Context, deps *Dependencies, body chatPostBody) (*chat.Message, string, error) {
	now := time.Now().UTC()
	// Match on the full (lowercased) message, not the
	// extractCommand token: the token is the first "/word" only,
	// so "/plan day" yields "/plan" and the case "/plan day"
	// label can never match on the token alone. Plain text never
	// reaches here (the handler returns early on Command == ""),
	// so a HasPrefix dispatch is safe.
	msg := strings.ToLower(strings.TrimSpace(body.Message))
	switch {
	case strings.HasPrefix(msg, "/plan day"):
		cmd := "/plan day"
		title := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(body.Message), cmd))
		if title == "" || title == strings.TrimSpace(body.Message) {
			title = "Daily plan"
		}
		result, err := deps.StudyService.Propose(ctx, "chat", studyservice.ProposeInput{
			CourseID:   "",
			Title:      title,
			BodyMD:     "Submitted via Dashboard chat.",
			TargetDate: now.Format("2006-01-02"),
		})
		if err != nil {
			return nil, "", err
		}
		return &chat.Message{
			ThreadID:   body.ThreadID,
			SenderType: chat.SenderAgent,
			BodyMD:     "Записал план в лоток предложений. Откройте Dashboard tray, чтобы принять.",
			Command:    "/plan day",
			ResultRef:  result.Proposal.ID,
			CreatedAt:  now,
		}, result.Proposal.ID, nil
	case strings.HasPrefix(msg, "/help"):
		return &chat.Message{
			ThreadID:   body.ThreadID,
			SenderType: chat.SenderAgent,
			BodyMD:     "Команды: /plan day — предложить план на сегодня; /help — эта справка.",
			Command:    "/help",
			CreatedAt:  now,
		}, "", nil
	default:
		return &chat.Message{
			ThreadID:   body.ThreadID,
			SenderType: chat.SenderAgent,
			BodyMD:     "Принято. На текущем MVP поддерживаются команды /plan day и /help.",
			CreatedAt:  now,
		}, "", nil
	}
}

// extractCommand returns the leading "/xxx" token of a message,
// or "" for plain text. Whitespace-trimmed.
func extractCommand(msg string) string {
	msg = strings.TrimSpace(msg)
	if !strings.HasPrefix(msg, "/") {
		return ""
	}
	idx := strings.IndexAny(msg, " \t\n")
	if idx < 0 {
		return msg
	}
	return msg[:idx]
}

// publishChat fans a single message out to the WS topics so the
// Dashboard updates live: "chat" keeps the legacy shape (any
// thread, no user filter); "dashboard-chat" carries the T9 per-user
// contract — user_id routes the event through the hub's per-user
// filter, thread_id lets the panel ignore other threads, and
// sender_type tells the UI when to drop the "agent is typing"
// indicator. nil-safe.
func publishChat(ctx context.Context, deps *Dependencies, m *chat.Message) {
	if deps.WSHub == nil || m == nil {
		return
	}
	deps.WSHub.Publish(ctx, ws.Event{
		Topic: "chat",
		Body: map[string]any{
			"thread_id": m.ThreadID,
			"message":   m,
		},
	})
	deps.WSHub.Publish(ctx, ws.Event{
		Topic: "dashboard-chat",
		Body: map[string]any{
			"user_id":     m.UserID,
			"thread_id":   m.ThreadID,
			"sender_type": string(m.SenderType),
			"message":     m,
		},
	})
}
