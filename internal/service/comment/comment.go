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

// Service is the dependency holder.
type Service struct {
	Repo         Repository
	Hub          ws.Hub
	AuthorLookup AuthorLookup
}

// New returns a Service with optional Hub/AuthorLookup (nil = no-op).
func New(repo Repository, hub ws.Hub, lookup AuthorLookup) *Service {
	return &Service{Repo: repo, Hub: hub, AuthorLookup: lookup}
}

// Add creates a comment and publishes a WS event.
//
// The caller supplies the full comment minus ID/created_at; the service
// assigns those.
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
	return got, nil
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
