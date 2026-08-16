// Package comment provides business logic for comments.
//
// Phase 3.7 ships Add (wraps repository.Create + WS event),
// ListByTarget, and MentionsForComment. The repository itself is in
// internal/storage/sqlite/comment_repo.go.
package comment

import (
	"context"
	"errors"
	"fmt"

	"github.com/ramgml/orenda/internal/api/ws"
	"github.com/ramgml/orenda/internal/domain/comment"
	notifierservice "github.com/ramgml/orenda/internal/service/notifier"
)

// Sentinel errors.
var (
	ErrNotFound     = errors.New("comment service: not found")
	ErrInvalidInput = errors.New("comment service: invalid input")
)

// Repository is the small surface the service needs from the storage.
type Repository interface {
	Create(ctx context.Context, c *comment.Comment) (*comment.Comment, error)
	GetByID(ctx context.Context, id string) (*comment.Comment, error)
	ListByTarget(ctx context.Context, targetType comment.TargetType, targetID string) ([]*comment.Comment, error)
	MentionsForComment(ctx context.Context, commentID string) ([]*comment.Mention, error)
}

// AuthorLookup resolves an author_type/author_id pair into something
// the notifier can route. Phase 6's notifier uses this; for now we
// just pass it through unchanged.
type AuthorLookup interface {
	ResolveAuthor(ctx context.Context, authorType comment.AuthorType, authorID string) (displayName string, err error)
}

// TaskOwnerResolver lets the comment service find the recipient
// for a `task.commented` notification. The concrete type is
// wired by cmd/orenda — for now we keep the seam small: any
// implementation that maps a task_id to an owner_user_id works.
type TaskOwnerResolver interface {
	OwnerForTask(ctx context.Context, taskID string) (userID string, taskTitle string, err error)
}

// Notifier is the slim seam the comment service uses to fan out
// `task.commented` events. nil disables the event.
type Notifier interface {
	Notify(ctx context.Context, e notifierservice.Event) error
}

// Service is the dependency holder.
type Service struct {
	Repo         Repository
	Hub          ws.Hub
	AuthorLookup AuthorLookup
	// Phase Wave 4 PR 2: notifier + task-owner lookup. Both
	// nil-safe (the comment is still created; only the
	// downstream event is skipped).
	Notifier          Notifier
	TaskOwnerResolver TaskOwnerResolver
}

// New returns a Service with optional Hub/AuthorLookup (nil = no-op).
func New(repo Repository, hub ws.Hub, lookup AuthorLookup) *Service {
	return &Service{Repo: repo, Hub: hub, AuthorLookup: lookup}
}

// Add creates a comment and publishes a WS event.
//
// The caller supplies the full comment minus ID/created_at; the service
// assigns those.
//
// Phase Wave 4 PR 2: also fires a `task.commented` notification to
// the task's owner when the comment is on a task. We skip the
// notification when the author IS the recipient (no point
// notifying yourself) and when the lookup is unwired.
func (s *Service) Add(ctx context.Context, c *comment.Comment) (*comment.Comment, error) {
	if c == nil {
		return nil, ErrInvalidInput
	}
	if err := c.Validate(); err != nil {
		return nil, err
	}
	got, err := s.Repo.Create(ctx, c)
	if err != nil {
		return nil, fmt.Errorf("comment service.Add: %w", err)
	}

	if s.Hub != nil {
		mentions, _ := s.Repo.MentionsForComment(ctx, got.ID)
		s.Hub.Publish(ctx, ws.Event{
			Topic: "comments",
			Body: map[string]any{
				"type":     "comment.added",
				"comment":  got,
				"mentions": mentions,
			},
		})
	}

	// task.commented — fan out to the task owner. Only when the
	// comment lives on a task target (comments on wiki pages etc.
	// are out of scope for this event).
	if s.Notifier != nil && s.TaskOwnerResolver != nil && got.TargetType == comment.TargetTask {
		if ownerID, title, err := s.TaskOwnerResolver.OwnerForTask(ctx, got.TargetID); err == nil && ownerID != "" {
			if ownerID != string(got.AuthorType)+":"+got.AuthorID {
				// Author is identified by (type, id); the
				// owner lookup returns the user_id. They
				// don't match unless the author IS the
				// owner; skipping self-notifies is just
				// polite.
				_ = s.Notifier.Notify(ctx, notifierservice.Event{
					Type:       "task.commented",
					UserID:     ownerID,
					TargetType: "task",
					TargetID:   got.TargetID,
					Title:      "New comment on: " + title,
					Body:       truncate(got.BodyMD, 200),
					Link:       "/tasks/" + got.TargetID,
					DedupKey:   "task.commented:" + got.ID,
				})
			}
		}
	}

	return got, nil
}

// truncate trims s to max runes (chars), appending an ellipsis when
// it clips. Used for the notifier body so we don't ship a 10KB
// comment in every channel's preview. Rune-aware so multi-byte
// characters (café) don't get cut mid-codepoint.
func truncate(s string, max int) string {
	if max <= 0 {
		return s
	}
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max]) + "…"
}

// ListByTarget returns every comment for a (target_type, target_id) pair
// in chronological order (the repository handles ORDER BY created_at).
func (s *Service) ListByTarget(ctx context.Context, targetType comment.TargetType, targetID string) ([]*comment.Comment, error) {
	if targetID == "" {
		return nil, ErrInvalidInput
	}
	got, err := s.Repo.ListByTarget(ctx, targetType, targetID)
	if err != nil {
		return nil, err
	}
	return got, nil
}

// MentionsForComment returns the mentions extracted when the comment was
// created. Used by the notifier (Phase 6) and by the UI to render
// notification badges.
func (s *Service) MentionsForComment(ctx context.Context, id string) ([]*comment.Mention, error) {
	if id == "" {
		return nil, ErrInvalidInput
	}
	got, err := s.Repo.MentionsForComment(ctx, id)
	if err != nil {
		return nil, err
	}
	return got, nil
}
