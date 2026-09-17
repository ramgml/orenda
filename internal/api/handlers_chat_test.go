package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ramgml/orenda/internal/api/ws"
	"github.com/ramgml/orenda/internal/domain/chat"
	"github.com/ramgml/orenda/internal/service/chatdialog"
	studysvc "github.com/ramgml/orenda/internal/service/study"
	sqlite "github.com/ramgml/orenda/internal/storage/sqlite"
)

// fakeChatMessages is an in-memory MessageRepository for the chat
// handler tests. The repo contract is tiny, so an unsynchronized
// slice is fine — tests are single-threaded.
type fakeChatMessages struct {
	msgs []*chat.Message
}

func (f *fakeChatMessages) Create(_ context.Context, m *chat.Message) error {
	if m.ID == "" {
		m.ID = chatMsgFakeID(len(f.msgs))
	}
	f.msgs = append(f.msgs, m)
	return nil
}

func chatMsgFakeID(n int) string {
	return fmt.Sprintf("msg-%03d", n+1)
}

func (f *fakeChatMessages) ByID(_ context.Context, id string) (*chat.Message, error) {
	for _, m := range f.msgs {
		if m.ID == id {
			return m, nil
		}
	}
	return nil, chat.ErrNotFound
}

func (f *fakeChatMessages) Last(_ context.Context, userID, threadID string) (*chat.Message, error) {
	var last *chat.Message
	for _, m := range f.msgs {
		if m.UserID == userID && m.ThreadID == threadID {
			last = m
		}
	}
	return last, nil
}

func (f *fakeChatMessages) ListByUserThread(_ context.Context, userID, threadID string, _ int) ([]*chat.Message, error) {
	out := make([]*chat.Message, 0)
	for _, m := range f.msgs {
		if m.UserID == userID && m.ThreadID == threadID {
			out = append(out, m)
		}
	}
	return out, nil
}

// PendingThreads mirrors the sqlite repo's queue derivation: the
// (user, thread) keys whose last message is a user question.
func (f *fakeChatMessages) PendingThreads(_ context.Context) ([]chatdialog.PendingThreadKey, error) {
	lastByThread := map[string]*chat.Message{}
	for _, m := range f.msgs {
		key := m.UserID + "\x00" + m.ThreadID
		if cur, ok := lastByThread[key]; !ok || m.CreatedAt.After(cur.CreatedAt) {
			lastByThread[key] = m
		}
	}
	out := make([]chatdialog.PendingThreadKey, 0, len(lastByThread))
	for _, m := range lastByThread {
		if m.SenderType == chat.SenderUser {
			out = append(out, chatdialog.PendingThreadKey{UserID: m.UserID, ThreadID: m.ThreadID})
		}
	}
	return out, nil
}

// fakeChatThreads is an in-memory ThreadRepository for the per-user
// replay tests.
type fakeChatThreads struct {
	owned map[string]map[string]bool // user -> thread
}

func (f *fakeChatThreads) Upsert(_ context.Context, userID, threadID string) error {
	if f.owned == nil {
		f.owned = map[string]map[string]bool{}
	}
	if f.owned[userID] == nil {
		f.owned[userID] = map[string]bool{}
	}
	f.owned[userID][threadID] = true
	return nil
}

func (f *fakeChatThreads) Owned(_ context.Context, userID, threadID string) (bool, error) {
	return f.owned[userID][threadID], nil
}

// chatUserIDCtx attaches a signed-in user identity — the handlers
// resolve the user via userIDFromCtx.
func chatUserIDCtx(userID string) context.Context {
	return WithIdentity(context.Background(), &Identity{UserID: userID})
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
	req = req.WithContext(chatUserIDCtx("user-1"))
	w := httptest.NewRecorder()
	handler(w, req)

	require.Equal(t, http.StatusCreated, w.Code)
	var resp chatPostResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, chat.SenderUser, resp.UserMessage.SenderType)
	assert.Equal(t, chat.SenderAgent, resp.AgentMessage.SenderType)
	assert.Contains(t, resp.AgentMessage.BodyMD, "/plan day")
	require.Len(t, deps.ChatMessages.(*fakeChatMessages).msgs, 2)
	assert.Equal(t, "user-1", resp.UserMessage.UserID)
	assert.Equal(t, "user-1", resp.AgentMessage.UserID)
}

// TestChat_PlainTextStaysPending pins the T9 dialog contract:
// plain text (no leading "/") is persisted as the user's message
// and left pending — the agent_message stays null and only ONE row
// is written; the dashboard agent answers it later via
// /agent/chat.
func TestChat_PlainTextStaysPending(t *testing.T) {
	t.Parallel()
	deps := &Dependencies{
		ChatMessages: &fakeChatMessages{},
		WSHub:        ws.NopHub{},
	}
	handler := postDashboardChatHandler(deps)
	body, _ := json.Marshal(map[string]string{"message": "hi there"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/dashboard/chat", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(chatUserIDCtx("user-1"))
	w := httptest.NewRecorder()
	handler(w, req)
	require.Equal(t, http.StatusCreated, w.Code)
	var resp chatPostResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Nil(t, resp.AgentMessage, "no agent echo for plain text")
	assert.True(t, resp.Pending, "plain text leaves the thread pending")
	msgs := deps.ChatMessages.(*fakeChatMessages).msgs
	require.Len(t, msgs, 1, "only the user message is persisted")
	assert.Equal(t, chat.SenderUser, msgs[0].SenderType)
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
		req = req.WithContext(chatUserIDCtx("user-1"))
		w := httptest.NewRecorder()
		handler(w, req)
		assert.Equal(t, http.StatusBadRequest, w.Code, "msg=%q", msg)
	}
}

// TestChat_POSTRequiresUser pins the T9 auth contract: the
// dashboard chat writes are per-user, an anonymous POST is 401.
func TestChat_POSTRequiresUser(t *testing.T) {
	t.Parallel()
	deps := &Dependencies{
		ChatMessages: &fakeChatMessages{},
		WSHub:        ws.NopHub{},
	}
	handler := postDashboardChatHandler(deps)
	body, _ := json.Marshal(map[string]string{"message": "hello"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/dashboard/chat", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler(w, req)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
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
	req = req.WithContext(chatUserIDCtx("user-1"))
	w := httptest.NewRecorder()
	handler(w, req)
	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
}

// TestChat_GETReplaysThread pins the read side: GET /{thread}
// returns the signed-in user's messages for that thread in the
// order they were persisted.
func TestChat_GETReplaysThread(t *testing.T) {
	t.Parallel()
	repo := &fakeChatMessages{}
	threads := &fakeChatThreads{}
	deps := &Dependencies{
		ChatMessages: repo,
		ChatThreads:  threads,
		WSHub:        ws.NopHub{},
	}
	// Seed two messages on the "default" thread for user-1.
	_ = repo.Create(context.Background(), &chat.Message{
		UserID: "user-1", ThreadID: "default", SenderType: chat.SenderUser, BodyMD: "/help",
	})
	_ = repo.Create(context.Background(), &chat.Message{
		UserID: "user-1", ThreadID: "default", SenderType: chat.SenderAgent, BodyMD: "ok",
	})
	_ = threads.Upsert(context.Background(), "user-1", "default")

	handler := getDashboardChatHandler(deps)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/dashboard/chat/default", nil)
	req = req.WithContext(chatUserIDCtx("user-1"))
	w := httptest.NewRecorder()
	handler(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	var resp struct {
		Messages []*chat.Message `json:"messages"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Len(t, resp.Messages, 2)
}

// TestChat_GETScopedToOwner pins the T9 isolation contract: a
// thread another user opened (or one that does not exist) replays
// as an empty history — 200 with an empty list, never the other
// user's messages.
func TestChat_GETScopedToOwner(t *testing.T) {
	t.Parallel()
	repo := &fakeChatMessages{}
	threads := &fakeChatThreads{}
	deps := &Dependencies{
		ChatMessages: repo,
		ChatThreads:  threads,
		WSHub:        ws.NopHub{},
	}
	_ = repo.Create(context.Background(), &chat.Message{
		UserID: "user-1", ThreadID: "default", SenderType: chat.SenderUser, BodyMD: "secret",
	})
	_ = threads.Upsert(context.Background(), "user-1", "default")

	handler := getDashboardChatHandler(deps)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/dashboard/chat/default", nil)
	req = req.WithContext(chatUserIDCtx("user-2"))
	w := httptest.NewRecorder()
	handler(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	var resp struct {
		Messages []*chat.Message `json:"messages"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Empty(t, resp.Messages)
}

// TestChat_PlanDayCreatesProposal pins the T9 DoD end-to-end: the
// user types "/plan day" in the dashboard chat, the command runs
// the study-proposal pipeline, and the referenced proposal row
// exists with status='pending' — visible in the proposals tray
// without any manual task creation.
func TestChat_PlanDayCreatesProposal(t *testing.T) {
	t.Parallel()
	db := copyInternalTemplateDB(t)
	studySvc := studysvc.New(
		sqlite.NewStudyProposalRepository(db),
		sqlite.NewTaskRepository(db),
		nil, nil,
	)
	// The chat pipeline stamps proposals with the actor literal
	// "chat"; study_proposals.created_by_agent has a FK to
	// agents(id), satisfied at runtime by sqlite.EnsureChatActor
	// (the ensureOwner precedent — migrations must not create
	// users). Exercise the production path: call the same runtime
	// ensure runServe calls.
	ctx := context.Background()
	require.NoError(t, sqlite.EnsureChatActor(ctx, db))

	deps := &Dependencies{
		ChatMessages: sqlite.NewChatMessageRepository(db),
		StudyService: studySvc,
		WSHub:        ws.NopHub{},
	}

	handler := postDashboardChatHandler(deps)
	body, _ := json.Marshal(map[string]string{"message": "/plan day"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/dashboard/chat", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(chatUserIDCtx("user-1"))
	w := httptest.NewRecorder()
	handler(w, req)
	require.Equal(t, http.StatusCreated, w.Code, "body=%s", w.Body.String())

	var resp chatPostResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.NotEmpty(t, resp.ResultRef, "plan day returns a proposal reference")
	require.NotNil(t, resp.AgentMessage)
	assert.Equal(t, resp.ResultRef, resp.AgentMessage.ResultRef)

	// The tray read-back: the proposal row the result_ref points
	// at exists and is pending.
	var status string
	err := db.QueryRowContext(context.Background(),
		`SELECT status FROM study_proposals WHERE id = ?`, resp.ResultRef,
	).Scan(&status)
	require.NoError(t, err, "proposal row must exist for the tray")
	assert.Equal(t, "pending", status)
}
