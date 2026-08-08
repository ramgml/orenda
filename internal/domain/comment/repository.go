package comment

import "context"

// Repository persists and retrieves Comments and Mentions.
type Repository interface {
	// Create inserts c and writes any mentions extracted from c.BodyMD
	// into the mentions table. Returns the comment with CreatedAt populated.
	Create(ctx context.Context, c *Comment) (*Comment, error)

	// GetByID returns the comment or ErrNotFound.
	GetByID(ctx context.Context, id string) (*Comment, error)

	// ListByTarget returns all comments for a (target_type, target_id)
	// pair, ordered by created_at ascending.
	ListByTarget(ctx context.Context, targetType TargetType, targetID string) ([]*Comment, error)

	// MentionsForComment returns every mention extracted from the comment.
	MentionsForComment(ctx context.Context, commentID string) ([]*Mention, error)
}
