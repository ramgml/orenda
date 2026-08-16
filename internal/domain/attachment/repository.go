package attachment

import "context"

// ProjectAttachment decorates Attachment with the title of the task it
// belongs to (when applicable). For project-level attachments the
// TaskTitle is empty.
type ProjectAttachment struct {
	Attachment
	TaskTitle string `json:"task_title,omitempty"`
}

// Repository persists and retrieves Attachments.
type Repository interface {
	Create(ctx context.Context, a *Attachment) error
	GetByID(ctx context.Context, id string) (*Attachment, error)
	ListByTarget(ctx context.Context, targetType TargetType, targetID string) ([]*Attachment, error)

	// ListByProject returns every attachment that belongs to a project
	// — both the ones attached directly to the project and the ones
	// attached to any of its tasks — newest first. Task attachments are
	// annotated with their task's title so the project attachments tab
	// can show a useful label.
	ListByProject(ctx context.Context, projectID string) ([]*ProjectAttachment, error)

	// FindBySHA256 returns the most recent attachment with the given
	// sha256 (used by the dedup check in Phase 3.8).
	FindBySHA256(ctx context.Context, sha string) (*Attachment, error)

	// Delete removes an attachment row. The file on disk is the caller's
	// responsibility (Phase 3.8 service does both).
	Delete(ctx context.Context, id string) error
}
