// Package notifier — real notifier facade.
//
// The facade fans events out across three surfaces:
//
//   - Subscriptions (bot_subscriptions table): per-user channel + events
//   - In-app inbox (notifications table) with dedup via dedup_key
//   - Dispatch to bots via internal/bot.Registry (Console, Telegram,
//     VK, Email, Webhook — Phase 10)
//   - Retry with exponential backoff (3 attempts) on bot errors
package notifier

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/ramgml/orenda/internal/api/ws"
	"github.com/ramgml/orenda/internal/bot"
)

// Event is the canonical notification envelope used by callers.
type Event struct {
	// Type is the event kind: "task.review_needed", "task.assigned_to_me",
	// "mention.created", "task.commented", "agent.offline", "backup.failed".
	Type string `json:"type"`

	// UserID is the recipient. Empty for system events.
	UserID string `json:"user_id,omitempty"`

	// Target identifies the object the event refers to.
	TargetType string `json:"target_type,omitempty"`
	TargetID   string `json:"target_id,omitempty"`

	// Title / Body are the human-readable text.
	Title string `json:"title"`
	Body  string `json:"body"`

	// Link is the in-app URL the notification should navigate to.
	Link string `json:"link,omitempty"`

	// DedupKey — when non-empty, only one unread notification per key is
	// kept. Used to collapse repeat events (e.g. agent.offline every 30s).
	DedupKey string `json:"dedup_key,omitempty"`

	// Meta is free-form extra payload, serialised as JSON.
	Meta map[string]string `json:"meta,omitempty"`
}

// Sentinel errors.
var (
	ErrInvalidInput = errors.New("notifier: invalid input")
)

// Notification is one row in the notifications table.
type Notification struct {
	ID         string     `json:"id"`
	UserID     string     `json:"user_id"`
	Type       string     `json:"type"`
	TargetType string     `json:"target_type,omitempty"`
	TargetID   string     `json:"target_id,omitempty"`
	Payload    string     `json:"payload"`
	ReadAt     *time.Time `json:"read_at,omitempty"`
	DedupKey   string     `json:"dedup_key"`
	CreatedAt  time.Time  `json:"created_at"`
}

// Subscription is one row in bot_subscriptions.
type Subscription struct {
	ID            string    `json:"id"`
	UserID        string    `json:"user_id"`
	BotType       string    `json:"bot_type"` // console | vk | telegram | email | webhook
	TargetAddress string    `json:"target_address"`
	Events        []string  `json:"events"` // e.g. ["task.review_needed", "mention.created"]
	Enabled       bool      `json:"enabled"`
	CreatedAt     time.Time `json:"created_at"`
}

// Subscribes reports whether the subscription cares about this event type.
func (s *Subscription) Subscribes(t string) bool {
	for _, e := range s.Events {
		if e == t {
			return true
		}
	}
	return false
}

// InboxRepository is the small surface the notifier needs for the
// notifications table.
type InboxRepository interface {
	// Upsert inserts a new notification. If a row with the same
	// (user_id, dedup_key) exists and is unread, it is replaced with
	// the new payload (the dedup semantic: collapses repeats).
	Upsert(ctx context.Context, n *Notification) error

	// ListByUser returns the most recent notifications, unread first.
	ListByUser(ctx context.Context, userID string, limit int) ([]*Notification, error)

	// MarkRead sets read_at for a notification.
	MarkRead(ctx context.Context, id string) error

	// UnreadCount returns the number of unread notifications per user.
	UnreadCount(ctx context.Context, userID string) (int, error)
}

// SubscriptionRepository is the small surface for bot_subscriptions.
type SubscriptionRepository interface {
	// ListForUserEvent returns enabled subscriptions for the user that
	// subscribe to the event type.
	ListForUserEvent(ctx context.Context, userID, eventType string) ([]*Subscription, error)

	// ListForUser returns every subscription for the user.
	ListForUser(ctx context.Context, userID string) ([]*Subscription, error)

	// ListByBotType returns every enabled subscription whose target
	// is for the given bot type. Used by Phase 21 to map an
	// inbound Telegram chat_id back to the user.
	ListByBotType(ctx context.Context, botType string) ([]*Subscription, error)
}

// Service is the dependency holder.
type Service struct {
	Inbox         InboxRepository
	Subscriptions SubscriptionRepository
	Bots          *bot.Registry
	Hub           ws.Hub

	// MaxRetries for bot dispatch (default 3).
	MaxRetries int
	// BaseBackoff for retries (default 100ms).
	BaseBackoff time.Duration
}

// New returns a notifier Service.
func New(in InboxRepository, subs SubscriptionRepository, bots *bot.Registry, hub ws.Hub) *Service {
	return &Service{
		Inbox:         in,
		Subscriptions: subs,
		Bots:          bots,
		Hub:           hub,
		MaxRetries:    3,
		BaseBackoff:   100 * time.Millisecond,
	}
}

// Notify dispatches an event: persists to the inbox (with dedup),
// publishes a WS event, and fans out to subscribed bots.
func (s *Service) Notify(ctx context.Context, e Event) error {
	if e.Type == "" {
		return ErrInvalidInput
	}
	if e.DedupKey == "" {
		e.DedupKey = fmt.Sprintf("%s:%s:%s", e.Type, e.TargetType, e.TargetID)
	}

	payloadJSON, err := json.Marshal(map[string]any{
		"title": e.Title,
		"body":  e.Body,
		"link":  e.Link,
		"meta":  e.Meta,
	})
	if err != nil {
		return fmt.Errorf("notifier: marshal payload: %w", err)
	}

	n := &Notification{
		UserID:     e.UserID,
		Type:       e.Type,
		TargetType: e.TargetType,
		TargetID:   e.TargetID,
		Payload:    string(payloadJSON),
		DedupKey:   e.DedupKey,
	}
	if s.Inbox != nil {
		if err := s.Inbox.Upsert(ctx, n); err != nil {
			return fmt.Errorf("notifier: inbox: %w", err)
		}
	}

	if s.Hub != nil {
		s.Hub.Publish(ctx, ws.Event{
			Topic: "notifications",
			Body: map[string]any{
				"type":  "notification",
				"event": e,
			},
		})
	}

	if s.Subscriptions != nil && s.Bots != nil {
		subs, err := s.Subscriptions.ListForUserEvent(ctx, e.UserID, e.Type)
		if err != nil {
			return fmt.Errorf("notifier: subs: %w", err)
		}
		// Render the canonical Message once per event; transports branch
		// on msg.Actions / msg.Kind instead of re-deriving the UI.
		tmpl := Render(e)
		for _, sub := range subs {
			if !sub.Enabled || !sub.Subscribes(e.Type) {
				continue
			}
			if b := s.Bots.Get(sub.BotType); b != nil {
				// Target = the recipient address for this subscription
				// (chat id, peer id, email…). CallbackID stays whatever
				// the template set (typically the task id), so callback
				// handlers can correlate a button press back to the
				// underlying task via the Action payload. We deliberately
				// copy the rendered msg here so per-bot overrides (if
				// any) don't leak between subscriptions.
				msg := tmpl
				msg.Target = sub.TargetAddress
				msg.Meta = e.Meta
				if err := s.sendWithRetry(ctx, b, sub.TargetAddress, msg); err != nil {
					// Log-only on retry exhaustion: the inbox row was
					// already written, so the user sees the notification
					// in-app. Real bot errors are observable via the log
					// record the handler emitted.
					_ = err
				}
			}
		}
	}
	return nil
}

// sendWithRetry retries the bot send with exponential backoff.
func (s *Service) sendWithRetry(ctx context.Context, b bot.Bot, target string, msg bot.Message) error {
	attempts := s.MaxRetries
	if attempts <= 0 {
		attempts = 3
	}
	base := s.BaseBackoff
	if base <= 0 {
		base = 100 * time.Millisecond
	}
	var last error
	for i := 0; i < attempts; i++ {
		if err := b.Send(ctx, target, msg); err != nil {
			last = err
			// Don't sleep after the final attempt.
			if i+1 < attempts {
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(time.Duration(math.Pow(2, float64(i))) * base):
				}
			}
			continue
		}
		return nil
	}
	return last
}
