// Package notifier — Phase 3.12 stub.
//
// Real notifier (Phase 6) dispatches to in-app inbox, bot subscriptions,
// and external channels (VK/TG/email/webhook). For now we just stage
// events in the api/ws.Hub so the UI sees them in real time. The
// interface is shaped for Phase 6 so we don't have to refactor callers.
package notifier

import (
	"context"
	"errors"
	"time"
)

// Event is the wire shape passed to the notifier. The Hub already has its
// own Event type; this is the cross-service seam so the notifier doesn't
// depend on internal/api/ws.
type Event struct {
	Topic     string
	UserID    string
	Body      any
	CreatedAt time.Time
}

// Notifier is the interface consumed by the service layer. The Phase 3
// implementation just logs to stderr + publishes to the WS hub; Phase 6
// adds bot dispatch.
type Notifier interface {
	Notify(ctx context.Context, e Event) error
}

// Sentinel errors.
var (
	ErrNoBackend = errors.New("notifier: no backend configured")
)

// HubEmitter is the small surface the notifier needs from the WS hub.
// *ws.Hub satisfies it.
type HubEmitter interface {
	Publish(ctx context.Context, topic string, e any)
}

// Stub is the Phase 3 implementation: publishes to a hub if available,
// otherwise no-ops. Real bot dispatch lands in Phase 6.
type Stub struct {
	Hub HubEmitter
}

// New returns a Stub. hub may be nil (no-op mode).
func New(hub HubEmitter) *Stub {
	return &Stub{Hub: hub}
}

// Notify publishes e to the configured hub.
func (s *Stub) Notify(_ context.Context, e Event) error {
	if s.Hub == nil {
		return ErrNoBackend
	}
	s.Hub.Publish(context.Background(), e.Topic, e)
	return nil
}

// Ensure Stub satisfies Notifier.
var _ Notifier = (*Stub)(nil)
