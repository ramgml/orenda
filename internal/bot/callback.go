// Package bot — callback handler (Phase 10.9).
//
// Translates interactive button presses (approve/reject) from Telegram
// and VK into task review actions. Each incoming callback carries a
// timestamp + nonce which we verify to defend against replay.
package bot

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// ReviewDecider is the surface the callback handler needs to act on a
// button press. The api package's task service satisfies this.
type ReviewDecider interface {
	Review(ctx context.Context, taskID, userID string, decision string, comment string) error
}

// UserResolver maps a bot user (telegram id, VK peer id) to an Orenda
// user id. Phase 10 keeps a single-owner model, so the resolver returns
// the owner's id regardless of input — multi-user lands with Phase 11+.
type UserResolver interface {
	ResolveOwner(ctx context.Context, botUserID string) (string, error)
}

// CallbackHandler processes bot callbacks.
type CallbackHandler struct {
	Decider  ReviewDecider
	Resolver UserResolver

	// ReplayWindow is the maximum age of an accepted callback.
	ReplayWindow time.Duration

	// seen tracks (nonce) values to defeat replays within ReplayWindow.
	mu   sync.Mutex
	seen map[string]time.Time
}

// NewCallbackHandler returns a handler.
func NewCallbackHandler(d ReviewDecider, r UserResolver) *CallbackHandler {
	return &CallbackHandler{
		Decider:      d,
		Resolver:     r,
		ReplayWindow: 5 * time.Minute,
		seen:         make(map[string]time.Time),
	}
}

// CallbackAction is the normalized form shared by Telegram + VK.
type CallbackAction struct {
	// Action is "approve" or "reject".
	Action string

	// TaskID is the task id the button refers to.
	TaskID string

	// Nonce is a unique value for replay protection (Telegram: callback
	// query id; VK: event_id).
	Nonce string

	// Timestamp is when the user pressed the button.
	Timestamp time.Time

	// BotUserID is the bot-side user (chat id, peer id).
	BotUserID string
}

// Sentinel errors.
var (
	ErrBadAction    = errors.New("bot callback: bad action")
	ErrReplay       = errors.New("bot callback: replay detected")
	ErrStale        = errors.New("bot callback: stale")
	ErrUnconfigured = errors.New("bot callback: not configured")
)

// Handle validates the callback and applies the review decision.
//
// Replay protection:
//   - Nonce must not have been seen within ReplayWindow.
//   - Timestamp must be within ReplayWindow of now.
func (h *CallbackHandler) Handle(ctx context.Context, a CallbackAction) error {
	if h.Decider == nil || h.Resolver == nil {
		return ErrUnconfigured
	}
	if a.Action != "approve" && a.Action != "reject" {
		return ErrBadAction
	}
	if a.TaskID == "" {
		return ErrBadAction
	}

	// Replay checks.
	now := time.Now()
	if now.Sub(a.Timestamp) > h.ReplayWindow {
		return ErrStale
	}
	if a.Nonce != "" {
		h.mu.Lock()
		if _, dup := h.seen[a.Nonce]; dup {
			h.mu.Unlock()
			return ErrReplay
		}
		h.seen[a.Nonce] = now
		// Prune old entries.
		for k, ts := range h.seen {
			if now.Sub(ts) > h.ReplayWindow {
				delete(h.seen, k)
			}
		}
		h.mu.Unlock()
	}

	owner, err := h.Resolver.ResolveOwner(ctx, a.BotUserID)
	if err != nil {
		return fmt.Errorf("bot callback: resolve owner: %w", err)
	}
	if err := h.Decider.Review(ctx, a.TaskID, owner, a.Action, ""); err != nil {
		return fmt.Errorf("bot callback: review: %w", err)
	}
	return nil
}

// GC sweeps expired nonces. Called by the bot loop occasionally.
func (h *CallbackHandler) GC() {
	h.mu.Lock()
	defer h.mu.Unlock()
	cutoff := time.Now().Add(-h.ReplayWindow)
	for k, ts := range h.seen {
		if ts.Before(cutoff) {
			delete(h.seen, k)
		}
	}
}
