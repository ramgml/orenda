// Package chat — the Dashboard user/agent chat domain (T9).
package chat

import (
	"context"
	"errors"
	"time"
)

// ErrNotFound is returned by lookups that hit no row (e.g.
// MessageRepository.ByID).
var ErrNotFound = errors.New("chat: not found")

// Message is one line in the user/agent chat on the Dashboard.
// Persisted in chat_messages (migrations 032 + 048). The WS topics
// "chat" and "dashboard-chat" carry new messages live; replay on
// page load is a SELECT via ListByUserThread.
type Message struct {
	ID string `json:"id"`
	// UserID is the owner of the conversation: who wrote a user
	// message and whose history an agent reply lands in. Empty in
	// legacy rows (pre-048); the replay treats "" as owned by
	// nobody.
	UserID     string     `json:"user_id"`
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

// MessageRepository persists chat messages. Implemented by
// *sqlite.chatMessageRepo; in-memory stubs keep service tests off
// SQLite.
type MessageRepository interface {
	// Create inserts a new message. ID is filled if empty.
	Create(ctx context.Context, m *Message) error
	// ByID fetches one message; chat.ErrNotFound when absent.
	// The agent reply flow resolves the pending question's
	// thread and owner from it.
	ByID(ctx context.Context, id string) (*Message, error)
	// Last returns the user's newest message on a thread (nil
	// when the thread has none). Pending is derived from it: a
	// thread is pending iff its last message is a user question.
	Last(ctx context.Context, userID, threadID string) (*Message, error)
	// ListByUserThread returns one user's messages on one thread
	// in chronological order (oldest first), capped at limit
	// (50 if limit <= 0). The dashboard replay uses it so users
	// never see each other's history.
	ListByUserThread(ctx context.Context, userID, threadID string, limit int) ([]*Message, error)
}

// ThreadRepository owns the (user_id, thread_id) mapping introduced
// by migration 047: POST /dashboard/chat upserts a row so only the
// opening user can replay the thread.
type ThreadRepository interface {
	// Upsert records that userID opened threadID (idempotent).
	Upsert(ctx context.Context, userID, threadID string) error
	// Owned reports whether userID has a row for threadID.
	Owned(ctx context.Context, userID, threadID string) (bool, error)
}
