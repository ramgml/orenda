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

	"github.com/go-chi/chi/v5"

	"github.com/ramgml/orenda/internal/api/ws"
	"github.com/ramgml/orenda/internal/domain/chat"
	"github.com/ramgml/orenda/internal/domain/user"
	"github.com/ramgml/orenda/internal/service/chatdialog"
)

// fakeChatUsers stubs the chatdialog.UserSource seam, backed by a
// plain display-name map.
type fakeChatUsers struct {
	names map[string]string
}

func (f *fakeChatUsers) GetByID(_ context.Context, id string) (*user.User, error) {
	name, ok := f.names[id]
	if !ok {
		return nil, user.ErrNotFound
	}
	return &user.User{ID: id, DisplayName: name}, nil
}

func chatUserSource(names map[string]string) chatdialog.UserSource {
	return &fakeChatUsers{names: names}
}

// withChatRouteParam sets the chi "message_id" route parameter on
// the request; direct handler tests bypass the router, so we mint
// the RouteContext ourselves.
func withChatRouteParam(r *http.Request, messageID string) *http.Request {
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("message_id", messageID)
	return r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
}
func chatAgentDeps() (*Dependencies, *fakeChatMessages) {
	repo := &fakeChatMessages{}
	deps := &Dependencies{
		ChatMessages: repo,
		ChatDialog: chatdialog.New(repo, chatUserSource(
			map[string]string{"user-1": "Alice", "user-2": "Bob"},
		)),
		WSHub: ws.NopHub{},
	}
	return deps, repo
}

// chatAgentCtx attaches an agent identity for RequireAgent-style
// handler tests (the handlers themselves only need the service).
func chatAgentCtx() context.Context {
	return WithIdentity(context.Background(), &Identity{AgentID: "agent-1"})
}

// TestChatAgent_PendingAndReplyHappy pins the full loop: a user
// question lands in the pending queue with display name + thread,
// the agent reply resolves it as the newest agent message on the
// same (user, thread).
func TestChatAgent_PendingAndReplyHappy(t *testing.T) {
	t.Parallel()
	deps, repo := chatAgentDeps()

	// The user asks via the dashboard POST path.
	ask := postDashboardChatHandler(deps)
	body, _ := json.Marshal(map[string]string{"message": "what about lunch"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/dashboard/chat", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(chatUserIDCtx("user-1"))
	w := httptest.NewRecorder()
	ask(w, req)
	require.Equal(t, http.StatusCreated, w.Code)

	pending := chatAgentPendingHandler(deps)
	req = httptest.NewRequest(http.MethodGet, "/api/v1/agent/chat/pending", nil)
	req = req.WithContext(chatAgentCtx())
	w = httptest.NewRecorder()
	pending(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	var queue struct {
		Pending []struct {
			Message         *chat.Message `json:"message"`
			UserID          string        `json:"user_id"`
			UserDisplayName string        `json:"user_display_name"`
			ThreadID        string        `json:"thread_id"`
		} `json:"pending"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &queue))
	require.Len(t, queue.Pending, 1)
	p := queue.Pending[0]
	assert.Equal(t, "user-1", p.UserID)
	assert.Equal(t, "Alice", p.UserDisplayName)
	assert.Equal(t, "default", p.ThreadID)
	require.NotNil(t, p.Message)
	assert.Equal(t, "what about lunch", p.Message.BodyMD)

	// The agent replies to that message id.
	reply := chatAgentReplyHandler(deps)
	rbody, _ := json.Marshal(map[string]string{"body_md": "12:30 at the canteen"})
	req = httptest.NewRequest(http.MethodPost, "/api/v1/agent/chat/reply", bytes.NewReader(rbody))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(chatAgentCtx())
	req = withChatRouteParam(req, p.Message.ID)
	w = httptest.NewRecorder()
	reply(w, req)
	require.Equal(t, http.StatusCreated, w.Code)
	var answer chat.Message
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &answer))
	assert.Equal(t, chat.SenderAgent, answer.SenderType)
	assert.Equal(t, "user-1", answer.UserID)
	assert.Equal(t, "default", answer.ThreadID)

	// Queue is empty after the reply; the thread replay ends with
	// the agent answer.
	req = httptest.NewRequest(http.MethodGet, "/api/v1/agent/chat/pending", nil)
	req = req.WithContext(chatAgentCtx())
	w = httptest.NewRecorder()
	pending(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &queue))
	assert.Empty(t, queue.Pending)

	last, err := repo.Last(context.Background(), "user-1", "default")
	require.NoError(t, err)
	require.NotNil(t, last)
	assert.Equal(t, chat.SenderAgent, last.SenderType)
}

// TestChatAgent_ReplyUnknownMessage pins 404 on a nonexistent
// message id.
func TestChatAgent_ReplyUnknownMessage(t *testing.T) {
	t.Parallel()
	deps, _ := chatAgentDeps()
	reply := chatAgentReplyHandler(deps)
	rbody, _ := json.Marshal(map[string]string{"body_md": "hi"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/agent/chat/reply", bytes.NewReader(rbody))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(chatAgentCtx())
	req = withChatRouteParam(req, "nope")
	w := httptest.NewRecorder()
	reply(w, req)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

// TestChatAgent_ReplyNotPending pins 409 when the question was
// already answered (its thread is no longer pending).
func TestChatAgent_ReplyNotPending(t *testing.T) {
	t.Parallel()
	deps, repo := chatAgentDeps()
	err := repo.Create(context.Background(), &chat.Message{
		UserID: "user-1", ThreadID: "default", SenderType: chat.SenderUser, BodyMD: "q",
	})
	require.NoError(t, err)
	// Answer it once by hand → the thread is resolved.
	_, err = deps.ChatDialog.Reply(context.Background(), "msg-001", "a1")
	require.NoError(t, err)

	reply := chatAgentReplyHandler(deps)
	rbody, _ := json.Marshal(map[string]string{"body_md": "a2"})
	req := withChatRouteParam(httptest.NewRequest(http.MethodPost, "/api/v1/agent/chat/reply", bytes.NewReader(rbody)), "msg-001")
	w := httptest.NewRecorder()
	reply(w, req)
	assert.Equal(t, http.StatusConflict, w.Code)
}

// TestChatAgent_ReplyEmptyBody pins 400 on an empty body_md.
func TestChatAgent_ReplyEmptyBody(t *testing.T) {
	t.Parallel()
	deps, repo := chatAgentDeps()
	err := repo.Create(context.Background(), &chat.Message{
		UserID: "user-1", ThreadID: "default", SenderType: chat.SenderUser, BodyMD: "q",
	})
	require.NoError(t, err)

	reply := chatAgentReplyHandler(deps)
	for _, md := range []string{"", "   "} {
		rbody, _ := json.Marshal(map[string]string{"body_md": md})
		req := httptest.NewRequest(http.MethodPost, "/api/v1/agent/chat/reply", bytes.NewReader(rbody))
		req.Header.Set("Content-Type", "application/json")
		req = req.WithContext(chatAgentCtx())
		req = withChatRouteParam(req, "msg-001")
		w := httptest.NewRecorder()
		reply(w, req)
		assert.Equal(t, http.StatusBadRequest, w.Code, "body_md=%q", md)
	}
}

// TestChatAgent_PendingPerUser pins the per-user queue: questions
// from two users both show up, each labelled with its own user and
// display name.
func TestChatAgent_PendingPerUser(t *testing.T) {
	t.Parallel()
	deps, _ := chatAgentDeps()
	ask := postDashboardChatHandler(deps)
	for _, u := range []string{"user-1", "user-2"} {
		body, _ := json.Marshal(map[string]string{"message": "question from " + u})
		req := httptest.NewRequest(http.MethodPost, "/api/v1/dashboard/chat", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req = req.WithContext(chatUserIDCtx(u))
		w := httptest.NewRecorder()
		ask(w, req)
		require.Equal(t, http.StatusCreated, w.Code)
	}

	pending := chatAgentPendingHandler(deps)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/agent/chat/pending", nil)
	req = req.WithContext(chatAgentCtx())
	w := httptest.NewRecorder()
	pending(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	var queue struct {
		Pending []struct {
			UserID          string `json:"user_id"`
			UserDisplayName string `json:"user_display_name"`
		} `json:"pending"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &queue))
	require.Len(t, queue.Pending, 2)
	byUser := map[string]string{}
	for _, p := range queue.Pending {
		byUser[p.UserID] = p.UserDisplayName
	}
	assert.Equal(t, "Alice", byUser["user-1"])
	assert.Equal(t, "Bob", byUser["user-2"])
}

// TestChatAgent_NotWiredReturns503 pins the nil-safe behaviour.
func TestChatAgent_NotWiredReturns503(t *testing.T) {
	t.Parallel()
	deps := &Dependencies{WSHub: ws.NopHub{}}
	pending := chatAgentPendingHandler(deps)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/agent/chat/pending", nil)
	w := httptest.NewRecorder()
	pending(w, req)
	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
}

// compile-time guards: the fakes must satisfy the seams.
