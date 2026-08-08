// Package bot — pluggable bot interface.
//
// Phase 6 ships the Console bot (writes to stderr); Phase 10 adds VK,
// Telegram, Email, Webhook bots on the same interface. The notifier-фасад
// uses the interface to fan out events without knowing which channels
// are configured.
package bot

import (
	"context"
	"errors"
	"sync"
	"time"
)

// Message is the wire shape sent to a bot. Phase 6 keeps this plain-text;
// Phase 10 adds structured templates (title, actions, attachments).
type Message struct {
	Title  string            `json:"title"`
	Body   string            `json:"body"`
	Kind   string            `json:"kind"` // e.g. "task.review_needed"
	Target string            `json:"target,omitempty"`
	Link   string            `json:"link,omitempty"`
	Meta   map[string]string `json:"meta,omitempty"`
}

// Bot is the interface every transport implements.
type Bot interface {
	// Name returns the bot's identifier (e.g. "console", "vk").
	Name() string

	// Start launches the bot (e.g. begin long-polling); called once at
	// server startup. Bots that don't have a start-up loop return nil.
	Start(ctx context.Context) error

	// Stop shuts down gracefully; called when the server shuts down.
	Stop(ctx context.Context) error

	// Send delivers a message to the given target (a chat id, email
	// address, webhook URL, or "console"). The target format is
	// bot-specific.
	Send(ctx context.Context, target string, msg Message) error
}

// Errors returned by Bots.
var (
	ErrBotUnavailable = errors.New("bot: unavailable")
	ErrTargetMissing  = errors.New("bot: missing target")
)

// Registry holds the configured bots.
type Registry struct {
	mu   sync.RWMutex
	bots map[string]Bot
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{bots: make(map[string]Bot)}
}

// Register adds a bot. Duplicate names are silently replaced (useful in
// tests that re-register the console bot).
func (r *Registry) Register(b Bot) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.bots[b.Name()] = b
}

// Get returns the bot with the given name, or nil if absent.
func (r *Registry) Get(name string) Bot {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.bots[name]
}

// List returns all registered bots (insertion order is not preserved).
func (r *Registry) List() []Bot {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Bot, 0, len(r.bots))
	for _, b := range r.bots {
		out = append(out, b)
	}
	return out
}

// ----------------------------------------------------------------------------
// Console bot (Phase 6.3): writes every message to stderr.
// ----------------------------------------------------------------------------

// Console is the simplest bot: it writes every Send to a writer. Useful
// in tests and when no external transport is configured.
type Console struct {
	// Out is the sink. Usually os.Stderr in production; bytes.Buffer in tests.
	Out interface {
		Write(p []byte) (n int, err error)
	}
	// Format renders a Message into bytes; override in tests to capture
	// structured output. Default: "title | body".
	Format func(Message) string
}

// Name implements Bot.
func (Console) Name() string { return "console" }

// Start implements Bot.
func (Console) Start(ctx context.Context) error { return nil }

// Stop implements Bot.
func (Console) Stop(ctx context.Context) error { return nil }

// Send implements Bot.
func (c Console) Send(ctx context.Context, target string, msg Message) error {
	if c.Out == nil {
		return ErrBotUnavailable
	}
	if c.Format != nil {
		_, err := c.Out.Write([]byte(c.Format(msg) + "\n"))
		return err
	}
	line := msg.Kind + " | " + msg.Title + " | " + msg.Body
	if msg.Target != "" {
		line += " -> " + msg.Target
	}
	_, err := c.Out.Write([]byte(line + "\n"))
	return err
}

// now is a test seam.
var now = time.Now
