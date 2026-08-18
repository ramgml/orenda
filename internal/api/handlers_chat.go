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
// Phase 32.11: messages start with "/" for commands. We support:
//   - "/plan day"     → triggers study-proposal pipeline. Returns the
//     proposal id in result_ref so the UI can link back.
//   - "/help"         → static text.
//   - plain text      → echoes back with an "agent" response that
//     acknowledges; this is the MVP-only path; free-
//     form dialogue is a future phase.
type chatPostBody struct {
	ThreadID string `json:"thread_id"`
	Message  string `json:"message"`
}

// chatPostResponse is the wire shape we return.
type chatPostResponse struct {
	UserMessage  *chat.Message `json:"user_message"`
	AgentMessage *chat.Message `json:"agent_message"`
	ResultRef    string        `json:"result_ref,omitempty"`
}

// postDashboardChatHandler — Phase 32.11 minimal chat endpoint.
//
// MVP scope (commands only):
//   - /plan day  → server calls StudyService.Propose with a
//     generic daily-plan payload. The /plan result lands in the
//     existing study-proposals tray (Phase 31.6) so the user can
//     accept/dismiss via the existing UI.
//   - /help      → static text.
//   - plain      → echoes a short "received" message.
//
// The endpoint persists both messages via ChatMessages so the
// Dashboard can replay history on page load (ListByThread). Live
// updates fan out over the WS topic "chat".
//
// UI side is out of scope for this PR (separate task).
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
		if body.ThreadID == "" {
			body.ThreadID = "default"
		}

		user := &chat.Message{
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

		agent, resultRef, err := dispatchChatCommand(r.Context(), deps, body)
		if err != nil {
			writeError(w, err)
			return
		}
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
		thread := chi.URLParam(r, "thread")
		if thread == "" {
			thread = "default"
		}
		msgs, err := deps.ChatMessages.ListByThread(r.Context(), thread, 50)
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
// Phase 32.11 MVP commands:
//   - "/plan day"  → StudyService.Propose with a generic daily-plan
//     payload. The /plan result lands in the study-
//     proposals tray (Phase 31.6). The result_ref is
//     the proposal id.
//   - "/help"      → static help.
//   - "" (plain)   → echo.
func dispatchChatCommand(ctx context.Context, deps *Dependencies, body chatPostBody) (*chat.Message, string, error) {
	now := time.Now().UTC()
	cmd := strings.ToLower(strings.TrimSpace(extractCommand(body.Message)))
	switch cmd {
	case "/plan day":
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
	case "/help":
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

// publishChat fans a single message out to the WS "chat" topic so
// the Dashboard updates live. nil-safe.
func publishChat(ctx context.Context, deps *Dependencies, m *chat.Message) {
	if deps.WSHub == nil {
		return
	}
	deps.WSHub.Publish(ctx, ws.Event{
		Topic: "chat",
		Body: map[string]any{
			"thread_id": m.ThreadID,
			"message":   m,
		},
	})
}
