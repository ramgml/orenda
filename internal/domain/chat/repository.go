package chat

import (
	"context"
	"time"
)

// Message is one line in the user/agent chat on the Dashboard.
// Persisted in chat_messages (migration 032). The WS topic
// "chat" carries new messages live; replay on page load is a
// SELECT via ListByThread.
type Message struct {
	ID         string     `json:"ID"`
	ThreadID   string     `json:"thread_id"`
	SenderType SenderType `json:"sender_type"`
	BodyMD     string     `json:"body_md"`
	// Command is set when the user message starts with "/" (e.g.
	// "/plan day"). The service dispatches the command; this is
	// the audit field on the persisted row.
	Command string `json:"command,omitempty"`
	// ResultRef is the id of the side-effect the command
	// produced (e.g. a study_proposal id for "/plan"). Empty for
	// plain text messages and for failed commands.
	ResultRef string    `json:"result_ref,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// SenderType is "user" or "agent".
type SenderType string

const (
	SenderUser  SenderType = "user"
	SenderAgent SenderType = "agent"
)

// MessageRepository persists chat messages.
type MessageRepository interface {
	// Create inserts a new message. ID is filled if empty.
	Create(ctx context.Context, m *Message) error
	// ListByThread returns the messages of one thread in
	// chronological order (oldest first), capped at limit
	// (50 if limit <= 0). Threads are keyed by an arbitrary
	// string — the UI uses a per-session id today.
	ListByThread(ctx context.Context, threadID string, limit int) ([]*Message, error)
}
