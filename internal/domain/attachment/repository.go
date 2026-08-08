package attachment

import "context"

// Repository persists and retrieves Attachments.
type Repository interface {
	Create(ctx context.Context, a *Attachment) error
	GetByID(ctx context.Context, id string) (*Attachment, error)
	ListByTarget(ctx context.Context, targetType TargetType, targetID string) ([]*Attachment, error)

	// FindBySHA256 returns the most recent attachment with the given
	// sha256 (used by the dedup check in Phase 3.8).
	FindBySHA256(ctx context.Context, sha string) (*Attachment, error)

	// Delete removes an attachment row. The file on disk is the caller's
	// responsibility (Phase 3.8 service does both).
	Delete(ctx context.Context, id string) error
}
