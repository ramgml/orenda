package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ramgml/orenda/internal/api/ws"
	"github.com/ramgml/orenda/internal/domain/chat"
)

// fakeChatMessages is an in-memory MessageRepository for the
// chat handler tests. The repo contract is tiny (Create + List),
// so an unsynchronized map is fine — tests are single-threaded.
type fakeChatMessages struct {
	msgs []*chat.Message
}

func (f *fakeChatMessages) Create(_ context.Context, m *chat.Message) error {
	f.msgs = append(f.msgs, m)
	return nil
}

func (f *fakeChatMessages) ListByThread(_ context.Context, threadID string, _ int) ([]*chat.Message, error) {
	out := make([]*chat.Message, 0)
	for _, m := range f.msgs {
		if m.ThreadID == threadID {
			out = append(out, m)
		}
	}
	return out, nil
}

// TestChat_ExtractCommand pins the command-token parser.
func TestChat_ExtractCommand(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in, want string
	}{
		{"/plan day", "/plan"},
		{"/plan", "/plan"},
		{"/plan\tday", "/plan"},
		{"/help", "/help"},
		{"/help now", "/help"},
		{"  /plan day  ", "/plan"},
		{"plain text", ""},
		{"", ""},
		{"/", "/"},
	}
	for _, tc := range cases {
		got := extractCommand(tc.in)
		assert.Equal(t, tc.want, got, "extractCommand(%q)", tc.in)
	}
}

// TestChat_PostPersistsAndDispatches exercises the happy path:
// POST /api/v1/dashboard/chat with /help → both messages
// persisted, agent reply echoes the static help text.
func TestChat_PostPersistsAndDispatches(t *testing.T) {
	t.Parallel()
	deps := &Dependencies{
		ChatMessages: &fakeChatMessages{},
		WSHub:        ws.NopHub{},
	}
	handler := postDashboardChatHandler(deps)
	body, _ := json.Marshal(map[string]string{"message": "/help"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/dashboard/chat", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler(w, req)

	require.Equal(t, http.StatusCreated, w.Code)
	var resp chatPostResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, chat.SenderUser, resp.UserMessage.SenderType)
	assert.Equal(t, chat.SenderAgent, resp.AgentMessage.SenderType)
	assert.Contains(t, resp.AgentMessage.BodyMD, "/plan day")
	require.Len(t, deps.ChatMessages.(*fakeChatMessages).msgs, 2)
}

// TestChat_PlainTextEchoes pins the MVP "free dialogue isn't
// supported yet" reply.
func TestChat_PlainTextEchoes(t *testing.T) {
	t.Parallel()
	deps := &Dependencies{
		ChatMessages: &fakeChatMessages{},
		WSHub:        ws.NopHub{},
	}
	handler := postDashboardChatHandler(deps)
	body, _ := json.Marshal(map[string]string{"message": "hi there"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/dashboard/chat", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler(w, req)
	require.Equal(t, http.StatusCreated, w.Code)
	var resp chatPostResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Empty(t, resp.AgentMessage.Command, "plain text has no command")
}

// TestChat_RejectsEmpty pins the input validation.
func TestChat_RejectsEmpty(t *testing.T) {
	t.Parallel()
	deps := &Dependencies{
		ChatMessages: &fakeChatMessages{},
		WSHub:        ws.NopHub{},
	}
	handler := postDashboardChatHandler(deps)
	for _, msg := range []string{"", "   ", "\n\t"} {
		body, _ := json.Marshal(map[string]string{"message": msg})
		req := httptest.NewRequest(http.MethodPost, "/api/v1/dashboard/chat", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		handler(w, req)
		assert.Equal(t, http.StatusBadRequest, w.Code, "msg=%q", msg)
	}
}

// TestChat_NotWiredReturns503 pins the nil-safe behaviour when
// the repo isn't wired (partial fixtures).
func TestChat_NotWiredReturns503(t *testing.T) {
	t.Parallel()
	deps := &Dependencies{
		ChatMessages: nil,
		WSHub:        ws.NopHub{},
	}
	handler := postDashboardChatHandler(deps)
	body, _ := json.Marshal(map[string]string{"message": "/help"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/dashboard/chat", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler(w, req)
	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
}

// TestChat_GETReplaysThread pins the read side: GET /{thread}
// returns the messages for that thread in the order they were
// persisted.
func TestChat_GETReplaysThread(t *testing.T) {
	t.Parallel()
	repo := &fakeChatMessages{}
	deps := &Dependencies{
		ChatMessages: repo,
		WSHub:        ws.NopHub{},
	}
	// Seed two messages on the "default" thread.
	_ = repo.Create(context.Background(), &chat.Message{
		ThreadID: "default", SenderType: chat.SenderUser, BodyMD: "/help",
	})
	_ = repo.Create(context.Background(), &chat.Message{
		ThreadID: "default", SenderType: chat.SenderAgent, BodyMD: "ok",
	})

	handler := getDashboardChatHandler(deps)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/dashboard/chat/default", nil)
	w := httptest.NewRecorder()
	handler(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	var resp struct {
		Messages []*chat.Message `json:"messages"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Len(t, resp.Messages, 2)
}
