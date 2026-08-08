package notifier_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ramgml/orenda/internal/service/notifier"
)

type memHub struct {
	events []any
	topics []string
}

func (m *memHub) Publish(_ context.Context, topic string, e any) {
	m.topics = append(m.topics, topic)
	m.events = append(m.events, e)
}

func TestStub_PublishesToHub(t *testing.T) {
	hub := &memHub{}
	n := notifier.New(hub)
	err := n.Notify(context.Background(), notifier.Event{
		Topic:  "tasks",
		UserID: "u-1",
		Body:   "hi",
	})
	require.NoError(t, err)
	require.Len(t, hub.events, 1)
	assert.Equal(t, "tasks", hub.topics[0])
}

func TestStub_NoBackendReturnsErr(t *testing.T) {
	n := notifier.New(nil)
	err := n.Notify(context.Background(), notifier.Event{Topic: "x", CreatedAt: time.Now()})
	assert.ErrorIs(t, err, notifier.ErrNoBackend)
}
