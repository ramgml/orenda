// Package comment holds the Comment domain entity.
//
// Comments are markdown bodies attached to a target (task / page / event / project).
// They support @mentions which the notifier (Phase 6) consumes.
package comment

import (
	"errors"
	"time"
)

// TargetType identifies what a comment is attached to.
type TargetType string

const (
	TargetTask    TargetType = "task"
	TargetPage    TargetType = "page"    // reserved for Phase 5
	TargetEvent   TargetType = "event"   // reserved for Phase 4
	TargetProject TargetType = "project" // Phase 11: project-level discussion
)

// AuthorType identifies the kind of author.
type AuthorType string

const (
	AuthorUser  AuthorType = "user"
	AuthorAgent AuthorType = "agent"
)

// Sentinel errors.
var (
	ErrNotFound     = errors.New("comment: not found")
	ErrInvalidInput = errors.New("comment: invalid input")
)

// Comment is the canonical entity.
type Comment struct {
	ID         string     `json:"id"`
	TargetType TargetType `json:"target_type"`
	TargetID   string     `json:"target_id"`
	AuthorType AuthorType `json:"author_type"`
	AuthorID   string     `json:"author_id"`
	BodyMD     string     `json:"body_md"`
	CreatedAt  time.Time  `json:"created_at"`
	EditedAt   *time.Time `json:"edited_at,omitempty"` // nil = never edited (Task 112)
}

// Mention records that a comment referenced a user or agent.
//
// Mentions live in a separate table for fan-out efficiency — Phase 6's
// notifier queries by target rather than scanning comment bodies.
type Mention struct {
	CommentID  string     `json:"comment_id"`
	TargetType TargetType `json:"target_type"` // 'user' or 'agent'
	TargetID   string     `json:"target_id"`
}

// Validate returns an error if Comment fields are inconsistent.
func (c *Comment) Validate() error {
	if c.TargetID == "" {
		return ErrInvalidInput
	}
	if c.AuthorID == "" {
		return ErrInvalidInput
	}
	if c.BodyMD == "" {
		return ErrInvalidInput
	}
	if c.TargetType == "" {
		c.TargetType = TargetTask
	}
	if c.AuthorType == "" {
		c.AuthorType = AuthorUser
	}
	return nil
}
