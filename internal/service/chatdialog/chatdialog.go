// Package chatdialog — the Dashboard agent dialog loop (T9).
//
// The user asks from the Dashboard chat pane; the message becomes
// pending (its thread's last message is now sender_type='user') and
// an agent answers later via the /agent/chat endpoints. Threads are
// (user_id, thread_id) pairs; the agent queue is derived, never
// stored, exactly like the lesson tutor (internal/service/tutor):
// a thread is pending iff its last message has sender='user'.
//
// Ask / Pending / Reply are the three use cases. Sentinels map onto
// HTTP statuses in the handlers: ErrNotFound → 404, ErrInvalidInput
// → 400, ErrConflict → 409.
package chatdialog

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ramgml/orenda/internal/domain/chat"
	"github.com/ramgml/orenda/internal/domain/user"
)

// Sentinel errors the API layer translates into HTTP statuses:
// ErrNotFound → 404, ErrInvalidInput → 400, ErrConflict → 409.
var (
	ErrNotFound     = errors.New("chatdialog service: not found")
	ErrInvalidInput = errors.New("chatdialog service: invalid input")
	ErrConflict     = errors.New("chatdialog service: conflict")
)

// Pending is one row of the agent's work queue: who asked, in which
// thread, and what they asked.
type Pending struct {
	Message         *chat.Message `json:"message"`
	UserID          string        `json:"user_id"`
	UserDisplayName string        `json:"user_display_name"`
	ThreadID        string        `json:"thread_id"`
}

// UserSource is the narrow user-domain seam the service needs to
// label the pending queue with display names. *sqlite.UserRepository
// satisfies it (it implements the full user.Repository); tests stub
// it.
type UserSource interface {
	// GetByID returns the user or an error when absent.
	GetByID(ctx context.Context, id string) (*user.User, error)
}

// Service implements the dashboard chat dialog use cases on top of
// the message repository. Stubs keep tests off SQLite.
type Service struct {
	Repo  chat.MessageRepository
	Users UserSource
}

// New wires a Service.
func New(repo chat.MessageRepository, users UserSource) *Service {
	return &Service{Repo: repo, Users: users}
}

// Ask records a user message on the user's thread. The thread
// becomes pending; a thread whose last message is already a user
// message stays pending — asking again is ErrConflict so the
// agent's queue never carries two open questions per thread.
func (s *Service) Ask(ctx context.Context, userID, threadID, bodyMD string) (*chat.Message, error) {
	if userID == "" || threadID == "" {
		return nil, ErrInvalidInput
	}
	bodyMD = strings.TrimSpace(bodyMD)
	if bodyMD == "" {
		return nil, ErrInvalidInput
	}
	last, err := s.Repo.Last(ctx, userID, threadID)
	if err != nil {
		return nil, err
	}
	if last != nil && last.SenderType == chat.SenderUser {
		return nil, ErrConflict
	}
	m := &chat.Message{
		UserID:     userID,
		ThreadID:   threadID,
		SenderType: chat.SenderUser,
		BodyMD:     bodyMD,
		Command:    chatCommand(bodyMD),
		CreatedAt:  time.Now().UTC(),
	}
	if err := s.Repo.Create(ctx, m); err != nil {
		return nil, fmt.Errorf("chatdialog.Ask: %w", err)
	}
	return m, nil
}

// PendingLister is the optional repo extension that exposes the
// pending queue: every (user, thread) whose last message is a user
// question. The sqlite repo implements it; in-memory stubs in
// service tests may too.
type PendingLister interface {
	// PendingThreads returns distinct (user_id, thread_id) keys
	// whose last message has sender_type='user'.
	PendingThreads(ctx context.Context) ([]PendingThreadKey, error)
}

// PendingThreadKey identifies a pending thread.
type PendingThreadKey struct {
	UserID   string
	ThreadID string
}

// Pending builds the agent's queue. Requires the repo to implement
// PendingLister (the sqlite repo does).
func (s *Service) Pending(ctx context.Context) ([]*Pending, error) {
	lister, ok := s.Repo.(PendingLister)
	if !ok {
		return nil, fmt.Errorf("chatdialog.Pending: repo does not support PendingThreads")
	}
	keys, err := lister.PendingThreads(ctx)
	if err != nil {
		return nil, fmt.Errorf("chatdialog.Pending: %w", err)
	}
	out := make([]*Pending, 0, len(keys))
	for _, k := range keys {
		last, err := s.Repo.Last(ctx, k.UserID, k.ThreadID)
		if err != nil {
			return nil, fmt.Errorf("chatdialog.Pending: %w", err)
		}
		if last == nil || last.SenderType != chat.SenderUser {
			continue // raced with a reply; not pending anymore
		}
		p := &Pending{
			Message:  last,
			UserID:   k.UserID,
			ThreadID: k.ThreadID,
		}
		if s.Users != nil && k.UserID != "" {
			if u, err := s.Users.GetByID(ctx, k.UserID); err == nil && u != nil {
				p.UserDisplayName = u.DisplayName
			}
		}
		out = append(out, p)
	}
	return out, nil
}

// Reply records the agent's answer on the thread the pending
// question came from. Unknown message id → ErrNotFound; the
// question no longer pending (already answered) → ErrConflict;
// empty body → ErrInvalidInput. On success the user sees the answer
// live via the WS topic "dashboard-chat".
func (s *Service) Reply(ctx context.Context, messageID, bodyMD string) (*chat.Message, error) {
	if messageID == "" {
		return nil, ErrInvalidInput
	}
	bodyMD = strings.TrimSpace(bodyMD)
	if bodyMD == "" {
		return nil, ErrInvalidInput
	}
	question, err := s.Repo.ByID(ctx, messageID)
	if err != nil {
		if errors.Is(err, chat.ErrNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	if question.SenderType != chat.SenderUser {
		return nil, ErrConflict
	}
	last, err := s.Repo.Last(ctx, question.UserID, question.ThreadID)
	if err != nil {
		return nil, err
	}
	if last == nil || last.ID != question.ID {
		return nil, ErrConflict
	}
	m := &chat.Message{
		UserID:     question.UserID,
		ThreadID:   question.ThreadID,
		SenderType: chat.SenderAgent,
		BodyMD:     bodyMD,
		CreatedAt:  time.Now().UTC(),
	}
	if err := s.Repo.Create(ctx, m); err != nil {
		return nil, fmt.Errorf("chatdialog.Reply: %w", err)
	}
	return m, nil
}

// chatCommand extracts the leading "/xxx" token of a message for
// the audit column; "" for plain text.
func chatCommand(msg string) string {
	msg = strings.TrimSpace(msg)
	if !strings.HasPrefix(msg, "/") {
		return ""
	}
	idx := strings.IndexAny(msg, " \t\n")
	if idx < 0 {
		return msg
	}
	return msg[:idx]
}
