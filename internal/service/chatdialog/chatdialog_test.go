// Package chatdialog — in-memory repo tests for the dialog loop.
//
// The service logic (pending derivation, conflict rules) runs on a
// stub repository so these stay off SQLite; the sqlite-backed
// derivation itself is covered by the storage migration tests.
package chatdialog

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ramgml/orenda/internal/domain/chat"
	"github.com/ramgml/orenda/internal/domain/user"
)

// memRepo is an in-memory MessageRepository + PendingLister.
type memRepo struct {
	msgs []*chat.Message
}

func (m *memRepo) Create(_ context.Context, msg *chat.Message) error {
	msg.ID = chatMsgID(len(m.msgs))
	m.msgs = append(m.msgs, msg)
	return nil
}

func chatMsgID(n int) string { return string(rune('a' + n)) }

func (m *memRepo) ByID(_ context.Context, id string) (*chat.Message, error) {
	for _, msg := range m.msgs {
		if msg.ID == id {
			return msg, nil
		}
	}
	return nil, chat.ErrNotFound
}

func (m *memRepo) Last(_ context.Context, userID, threadID string) (*chat.Message, error) {
	var last *chat.Message
	for _, msg := range m.msgs {
		if msg.UserID == userID && msg.ThreadID == threadID {
			last = msg
		}
	}
	return last, nil
}

func (m *memRepo) ListByUserThread(_ context.Context, userID, threadID string, _ int) ([]*chat.Message, error) {
	out := make([]*chat.Message, 0)
	for _, msg := range m.msgs {
		if msg.UserID == userID && msg.ThreadID == threadID {
			out = append(out, msg)
		}
	}
	return out, nil
}

func (m *memRepo) PendingThreads(_ context.Context) ([]PendingThreadKey, error) {
	lastByThread := map[string]*chat.Message{}
	for _, msg := range m.msgs {
		key := msg.UserID + "\x00" + msg.ThreadID
		if cur, ok := lastByThread[key]; !ok || msg.CreatedAt.After(cur.CreatedAt) {
			lastByThread[key] = msg
		}
	}
	out := make([]PendingThreadKey, 0, len(lastByThread))
	for _, msg := range lastByThread {
		if msg.SenderType == chat.SenderUser {
			out = append(out, PendingThreadKey{UserID: msg.UserID, ThreadID: msg.ThreadID})
		}
	}
	return out, nil
}

// memUsers stubs the UserSource seam.
type memUsers struct{ names map[string]string }

func (mu *memUsers) GetByID(_ context.Context, id string) (*user.User, error) {
	name, ok := mu.names[id]
	if !ok {
		return nil, user.ErrNotFound
	}
	return &user.User{ID: id, DisplayName: name}, nil
}

func newTestService() *Service {
	return New(&memRepo{}, &memUsers{names: map[string]string{"u1": "Alice"}})
}

// TestAsk_RecordsUserMessage pins the happy path: a trimmed user
// message lands with sender=user and the command token extracted.
func TestAsk_RecordsUserMessage(t *testing.T) {
	s := newTestService()
	m, err := s.Ask(context.Background(), "u1", "default", "  hello agent  ")
	require.NoError(t, err)
	assert.Equal(t, chat.SenderUser, m.SenderType)
	assert.Equal(t, "hello agent", m.BodyMD)
	assert.Equal(t, "u1", m.UserID)
	assert.Equal(t, "default", m.ThreadID)
	assert.Empty(t, m.Command)
}

// TestAsk_CommandExtracted pins the audit command token.
func TestAsk_CommandExtracted(t *testing.T) {
	s := newTestService()
	m, err := s.Ask(context.Background(), "u1", "default", "/plan day please")
	require.NoError(t, err)
	assert.Equal(t, "/plan", m.Command)
}

// TestAsk_RejectsBlankAndAnonymous pins input validation.
func TestAsk_RejectsBlankAndAnonymous(t *testing.T) {
	s := newTestService()
	for _, tc := range []struct{ user, thread, body string }{
		{"", "default", "hi"},
		{"u1", "", "hi"},
		{"u1", "default", "  "},
		{"u1", "default", ""},
	} {
		_, err := s.Ask(context.Background(), tc.user, tc.thread, tc.body)
		assert.ErrorIs(t, err, ErrInvalidInput, "%+v", tc)
	}
}

// TestAsk_DoublePendingConflict pins the one-open-question rule.
func TestAsk_DoublePendingConflict(t *testing.T) {
	s := newTestService()
	_, err := s.Ask(context.Background(), "u1", "default", "first")
	require.NoError(t, err)
	_, err = s.Ask(context.Background(), "u1", "default", "second")
	assert.ErrorIs(t, err, ErrConflict)
}

// TestAsk_NewQuestionAfterReply pins the loop: after the agent
// answers, the user can ask again.
func TestAsk_NewQuestionAfterReply(t *testing.T) {
	s := newTestService()
	q1, err := s.Ask(context.Background(), "u1", "default", "first")
	require.NoError(t, err)
	_, err = s.Reply(context.Background(), q1.ID, "answer")
	require.NoError(t, err)
	_, err = s.Ask(context.Background(), "u1", "default", "second")
	require.NoError(t, err)
}

// TestPending_ListsQueue pins the derived queue with display names.
func TestPending_ListsQueue(t *testing.T) {
	s := newTestService()
	_, err := s.Ask(context.Background(), "u1", "default", "need help")
	require.NoError(t, err)
	queue, err := s.Pending(context.Background())
	require.NoError(t, err)
	require.Len(t, queue, 1)
	assert.Equal(t, "u1", queue[0].UserID)
	assert.Equal(t, "Alice", queue[0].UserDisplayName)
	assert.Equal(t, "default", queue[0].ThreadID)
	assert.Equal(t, "need help", queue[0].Message.BodyMD)
}

// TestPending_ExcludesResolved pins that an answered thread leaves
// the queue.
func TestPending_ExcludesResolved(t *testing.T) {
	s := newTestService()
	q, err := s.Ask(context.Background(), "u1", "default", "need help")
	require.NoError(t, err)
	_, err = s.Reply(context.Background(), q.ID, "done")
	require.NoError(t, err)
	queue, err := s.Pending(context.Background())
	require.NoError(t, err)
	assert.Empty(t, queue)
}

// TestReply_HappyAndConflicts pins the reply contract: the answer
// lands on the question's thread and owner; unknown id →
// ErrNotFound; already-answered → ErrConflict; blank body →
// ErrInvalidInput; replying to an agent message → ErrConflict.
func TestReply_HappyAndConflicts(t *testing.T) {
	s := newTestService()
	q, err := s.Ask(context.Background(), "u1", "default", "question")
	require.NoError(t, err)

	a, err := s.Reply(context.Background(), q.ID, " answer ")
	require.NoError(t, err)
	assert.Equal(t, chat.SenderAgent, a.SenderType)
	assert.Equal(t, "answer", a.BodyMD)
	assert.Equal(t, "u1", a.UserID)
	assert.Equal(t, "default", a.ThreadID)

	_, err = s.Reply(context.Background(), q.ID, "again")
	assert.ErrorIs(t, err, ErrConflict)

	_, err = s.Reply(context.Background(), "missing", "x")
	assert.ErrorIs(t, err, ErrNotFound)

	_, err = s.Reply(context.Background(), a.ID, "to an agent row")
	assert.ErrorIs(t, err, ErrConflict)

	_, err = s.Reply(context.Background(), q.ID, "   ")
	assert.ErrorIs(t, err, ErrInvalidInput)
}
