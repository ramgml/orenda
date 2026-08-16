package notifier_test

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ramgml/orenda/internal/api/ws"
	"github.com/ramgml/orenda/internal/bot"
	"github.com/ramgml/orenda/internal/service/notifier"
)

// --- in-memory fakes ---

type memInbox struct {
	rows   []*notifier.Notification
	unread map[string]int
}

func newMemInbox() *memInbox {
	return &memInbox{unread: map[string]int{}}
}

func (m *memInbox) Upsert(_ context.Context, n *notifier.Notification) error {
	// Dedup: if a row with same (user, dedup_key) exists and is unread,
	// replace it.
	for i, x := range m.rows {
		if x.UserID == n.UserID && x.DedupKey == n.DedupKey && x.ReadAt == nil {
			m.rows[i] = n
			return nil
		}
	}
	m.rows = append(m.rows, n)
	m.unread[n.UserID]++
	return nil
}

func (m *memInbox) ListByUser(_ context.Context, userID string, _ int) ([]*notifier.Notification, error) {
	var out []*notifier.Notification
	for _, r := range m.rows {
		if r.UserID == userID {
			out = append(out, r)
		}
	}
	return out, nil
}

func (m *memInbox) MarkRead(_ context.Context, id string) error {
	for _, r := range m.rows {
		if r.ID == id && r.ReadAt == nil {
			now := time.Now()
			r.ReadAt = &now
			m.unread[r.UserID]--
		}
	}
	return nil
}

func (m *memInbox) UnreadCount(_ context.Context, userID string) (int, error) {
	return m.unread[userID], nil
}

type memSubs struct {
	subs []*notifier.Subscription
}

func (m *memSubs) ListByBotType(_ context.Context, bt string) ([]*notifier.Subscription, error) {
	var out []*notifier.Subscription
	for _, s := range m.subs {
		if s.BotType == bt {
			out = append(out, s)
		}
	}
	return out, nil
}

func (m *memSubs) ListForUserEvent(_ context.Context, userID, t string) ([]*notifier.Subscription, error) {
	var out []*notifier.Subscription
	for _, s := range m.subs {
		if s.UserID == userID && s.Enabled && s.Subscribes(t) {
			out = append(out, s)
		}
	}
	return out, nil
}

func (m *memSubs) ListForUser(_ context.Context, userID string) ([]*notifier.Subscription, error) {
	var out []*notifier.Subscription
	for _, s := range m.subs {
		if s.UserID == userID {
			out = append(out, s)
		}
	}
	return out, nil
}

type recordingBot struct {
	name  string
	sent  []bot.Message
	failN int // fail the first N Send calls
	calls int
}

func (b *recordingBot) Name() string                { return b.name }
func (b *recordingBot) Start(context.Context) error { return nil }
func (b *recordingBot) Stop(context.Context) error  { return nil }
func (b *recordingBot) Send(_ context.Context, target string, msg bot.Message) error {
	b.calls++
	if b.calls <= b.failN {
		return errors.New("transient")
	}
	msg.Target = target
	b.sent = append(b.sent, msg)
	return nil
}

type noopHub struct{}

func (noopHub) Publish(context.Context, ws.Event) {}
func (noopHub) Close()                            {}
func (noopHub) Subscribe(string, string) (<-chan ws.Event, ws.Unsubscribe) {
	ch := make(chan ws.Event, 1)
	return ch, func() { close(ch) }
}

// --- tests ---

func TestNotifier_InboxWithDedup(t *testing.T) {
	inbox := newMemInbox()
	subs := &memSubs{}
	reg := bot.NewRegistry()
	svc := notifier.New(inbox, subs, reg, noopHub{})

	e := notifier.Event{
		Type:     "task.review_needed",
		UserID:   "u-1",
		Title:    "Task X needs review",
		TargetID: "t-1",
		DedupKey: "task.review_needed:t-1",
	}
	require.NoError(t, svc.Notify(context.Background(), e))
	require.NoError(t, svc.Notify(context.Background(), e))
	require.NoError(t, svc.Notify(context.Background(), e))

	count, err := inbox.UnreadCount(context.Background(), "u-1")
	require.NoError(t, err)
	assert.Equal(t, 1, count, "dedup collapses 3 repeats into 1 unread")
}

func TestNotifier_FansOutToSubscribedBot(t *testing.T) {
	inbox := newMemInbox()
	subs := &memSubs{subs: []*notifier.Subscription{{
		ID:            "s-1",
		UserID:        "u-1",
		BotType:       "console",
		TargetAddress: "stderr",
		Events:        []string{"task.review_needed"},
		Enabled:       true,
	}}}
	reg := bot.NewRegistry()
	var buf bytes.Buffer
	reg.Register(bot.Console{Out: &buf})
	svc := notifier.New(inbox, subs, reg, noopHub{})

	err := svc.Notify(context.Background(), notifier.Event{
		Type:     "task.review_needed",
		UserID:   "u-1",
		Title:    "X",
		Body:     "body",
		DedupKey: "k",
	})
	require.NoError(t, err)
	assert.Contains(t, buf.String(), "task.review_needed")
	assert.Contains(t, buf.String(), "X")
}

func TestNotifier_RetrySucceedsAfterTransient(t *testing.T) {
	inbox := newMemInbox()
	subs := &memSubs{subs: []*notifier.Subscription{{
		UserID:        "u-1",
		BotType:       "rec",
		TargetAddress: "x",
		Events:        []string{"e"},
		Enabled:       true,
	}}}
	reg := bot.NewRegistry()
	rec := &recordingBot{name: "rec", failN: 2} // fail first 2, succeed on 3rd
	reg.Register(rec)
	svc := notifier.New(inbox, subs, reg, noopHub{})
	svc.BaseBackoff = time.Millisecond

	err := svc.Notify(context.Background(), notifier.Event{
		Type: "e", UserID: "u-1", Title: "t",
	})
	require.NoError(t, err)
	assert.Equal(t, 3, rec.calls, "expected 1 retry until success")
	assert.Len(t, rec.sent, 1)
}

func TestNotifier_RetryGivesUpAfterMaxAttempts(t *testing.T) {
	inbox := newMemInbox()
	subs := &memSubs{subs: []*notifier.Subscription{{
		UserID:        "u-1",
		BotType:       "rec",
		TargetAddress: "x",
		Events:        []string{"e"},
		Enabled:       true,
	}}}
	reg := bot.NewRegistry()
	rec := &recordingBot{name: "rec", failN: 100} // always fail
	reg.Register(rec)
	svc := notifier.New(inbox, subs, reg, noopHub{})
	svc.BaseBackoff = time.Millisecond

	// Notify succeeds (inbox written) even though bot fails — the
	// user's in-app notification is durable.
	err := svc.Notify(context.Background(), notifier.Event{
		Type: "e", UserID: "u-1", Title: "t",
	})
	require.NoError(t, err)
	assert.Equal(t, 3, rec.calls, "expected exactly MaxRetries attempts")
}

func TestNotifier_InvalidInput(t *testing.T) {
	svc := notifier.New(newMemInbox(), &memSubs{}, bot.NewRegistry(), noopHub{})
	err := svc.Notify(context.Background(), notifier.Event{})
	assert.ErrorIs(t, err, notifier.ErrInvalidInput)
}
