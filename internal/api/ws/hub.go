// Package ws implements the WebSocket hub + client for real-time push.
//
// Hub is in-process (Phase 2.5 — single owner, single process). Each
// authenticated user has at most one subscriber channel per topic. The
// service.TaskService publishes "tasks" events; subscribers receive them.
//
// Phase 3 will add agents and a multi-process-friendly design (Redis pub/sub
// or NATS) without changing the Hub interface.
package ws

import (
	"context"
	"sync"
)

// AllTopics lists every topic the hub accepts. Services publish to these
// topics (see service/notifier, service/task, service/wiki, etc.) and the
// WebSocket client subscribes to all of them at upgrade time so the UI
// receives live updates regardless of which surface changed.
//
// Phase 27.9: the WS handler used to subscribe only to "tasks", which
// silently dropped notifications / timers / calendar / wiki / comments /
// attachments / agents events. Single-owner deployment, no per-project
// filter required — fan out everything.
//
// New services that publish to the hub should add their topic here so the
// UI gets the live update without further wiring.
var AllTopics = []string{
	"tasks",
	"agents",
	"attachments",
	"comments",
	"events",
	"notifications",
	"projects",
	"timers",
	"wiki",
}

// Event is the wire shape sent over WebSocket. Subscribers receive the raw
// JSON-serialisable body — Phase 2 only uses map[string]any.
type Event struct {
	Topic string `json:"topic"`
	Body  any    `json:"body"`
}

// Unsubscribe cancels a subscription.
type Unsubscribe func()

// Hub is the seam used by services to publish events.
//
// Implementations must be safe for concurrent Publish + Subscribe from
// multiple goroutines. The default Hub is the in-process channel hub below.
type Hub interface {
	Publish(ctx context.Context, e Event)
	Subscribe(userID, topic string) (<-chan Event, Unsubscribe)
	// Close drains every active subscriber. Used by maintenance mode
	// (Phase 22.3) when the operator is restoring from a snapshot —
	// we close every WS so the clients reconnect and pick up the
	// restored DB.
	//
	// Note: implementations may make the hub unusable after Close;
	// callers should not expect Publish/Subscribe to work afterward.
	Close()
}

// channelHub is the default implementation. Each subscriber gets its own
// buffered channel; events published to a topic are fanned out to every
// subscriber for that topic. Filtering by userID happens at read time so a
// hub-wide index doesn't have to be kept in sync.
type channelHub struct {
	mu     sync.RWMutex
	subs   map[string]map[*chanEntry]struct{} // topic -> set of entries
	closed bool
}

// HubImpl is the exported alias so callers outside the package can name
// the concrete hub type (for tests, type assertions, etc.).
type HubImpl = channelHub

type chanEntry struct {
	ch     chan Event
	cancel context.CancelFunc
	ctx    context.Context
}

// NewHub returns a fresh in-process Hub.
func NewHub() Hub {
	return &channelHub{
		subs: make(map[string]map[*chanEntry]struct{}),
	}
}

// Publish broadcasts e to every subscriber of e.Topic.
//
// Subtle: Publish is non-blocking — if a subscriber's buffer is full, the
// event is dropped for that subscriber. Slow clients cannot back-pressure
// the publisher; the alternative would freeze the UI for every user.
func (h *channelHub) Publish(_ context.Context, e Event) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	if h.closed {
		return
	}
	for entry := range h.subs[e.Topic] {
		select {
		case entry.ch <- e:
		default:
			// drop on full
		}
	}
}

// Subscribe returns a buffered channel + an Unsubscribe function.
//
// The buffer size of 32 covers the common case (one user's UI receiving
// task events). When the buffer fills, Publish drops events for this
// subscriber — see Publish for rationale.
//
// The returned channel emits only events whose body has user_id == userID,
// or events without a user_id (system events). This filtering happens in a
// dedicated goroutine per subscription; cancelling via Unsubscribe stops
// the goroutine and closes the channel.
func (h *channelHub) Subscribe(userID, topic string) (<-chan Event, Unsubscribe) {
	raw := make(chan Event, 32)
	ctx, cancel := context.WithCancel(context.Background())
	entry := &chanEntry{ch: raw, cancel: cancel, ctx: ctx}

	h.mu.Lock()
	if h.subs[topic] == nil {
		h.subs[topic] = make(map[*chanEntry]struct{})
	}
	h.subs[topic][entry] = struct{}{}
	h.mu.Unlock()

	out := make(chan Event, 32)
	go func() {
		defer close(out)
		for {
			select {
			case <-ctx.Done():
				return
			case ev, ok := <-raw:
				if !ok {
					return
				}
				if uid, hasUID := extractUserID(ev.Body); hasUID && uid != userID {
					continue
				}
				select {
				case out <- ev:
				case <-ctx.Done():
					return
				}
			}
		}
	}()

	var once sync.Once
	unsub := func() {
		once.Do(func() {
			cancel()
			h.mu.Lock()
			if m := h.subs[topic]; m != nil {
				delete(m, entry)
				if len(m) == 0 {
					delete(h.subs, topic)
				}
			}
			h.mu.Unlock()
		})
	}
	return out, unsub
}

// extractUserID pulls "user_id" out of a map-shaped event body.
func extractUserID(body any) (string, bool) {
	m, ok := body.(map[string]any)
	if !ok {
		return "", false
	}
	v, ok := m["user_id"]
	if !ok {
		return "", false
	}
	s, ok := v.(string)
	return s, ok
}

// SubscriberCount returns the total number of active subscribers
// across every topic. Used by /api/v1/stats (Phase 24) to surface
// the live connection count.
//
// Cheap O(n) over the topic map; the hub holds tens to low hundreds
// of subscribers in the single-owner use case.
func (h *channelHub) SubscriberCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	if h.closed {
		return 0
	}
	n := 0
	for _, entries := range h.subs {
		n += len(entries)
	}
	return n
}

// Close drains all subscribers. Idempotent.
func (h *channelHub) Close() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return
	}
	h.closed = true
	for topic, entries := range h.subs {
		for e := range entries {
			e.cancel()
			close(e.ch)
		}
		delete(h.subs, topic)
	}
}

// NopHub drops every event. Useful for tests that don't need a hub.
type NopHub struct{}

// Publish implements Hub.
func (NopHub) Publish(context.Context, Event) {}

// Close is a no-op for NopHub (Phase 22.3: required by Hub interface).
func (NopHub) Close() {}

// Subscribe implements Hub — channel that closes immediately.
func (NopHub) Subscribe(string, string) (<-chan Event, Unsubscribe) {
	ch := make(chan Event)
	close(ch)
	return ch, func() {}
}
