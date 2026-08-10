// Package api — small service interfaces used by handlers.
//
// These interfaces let handlers depend on the *behaviour* they need
// without importing the concrete service packages (which would create
// import cycles through the api package).
package api

import (
	"context"
	"io"

	"github.com/ramgml/orenda/internal/domain/activity"
	"github.com/ramgml/orenda/internal/domain/attachment"
	"github.com/ramgml/orenda/internal/domain/comment"
	"github.com/ramgml/orenda/internal/domain/task"
)

// ReadSeekCloser is just an alias for the stdlib composite interface
// — *os.File satisfies it. We re-declare it here so the
// AttachmentService interface doesn't have to leak io into its
// import set.
type ReadSeekCloser = io.ReadSeekCloser

// CommentService is the slice of comment.Service that handlers use.
type CommentService interface {
	Add(ctx context.Context, c *comment.Comment) (*comment.Comment, error)
	ListByTarget(ctx context.Context, targetType comment.TargetType, targetID string) ([]*comment.Comment, error)
	MentionsForComment(ctx context.Context, id string) ([]*comment.Mention, error)
}

// AttachmentResult mirrors attachment.Service.StoreResult. Defined here
// (rather than imported) so the api package stays decoupled from the
// service package.
type AttachmentResult struct {
	Attachment *attachment.Attachment
	Duplicate  bool
}

// AttachmentService is the slice of attachment.Service that handlers use.
type AttachmentService interface {
	StoreFromBytes(
		ctx context.Context,
		targetType attachment.TargetType,
		targetID, filename, mime string,
		uploaderType attachment.UploaderType,
		uploaderID string,
		body io.Reader,
	) (*AttachmentResult, error)
	Get(ctx context.Context, id string) (*attachment.Attachment, error)
	ListByTarget(ctx context.Context, t attachment.TargetType, targetID string) ([]*attachment.Attachment, error)
	Delete(ctx context.Context, id string) error
	// Open returns the row and a ready-to-stream file handle. The
	// caller closes the file. Used by the download endpoint.
	Open(ctx context.Context, id string) (*attachment.Attachment, ReadSeekCloser, error)
}

// ActivityService is the slice of activity.Repository + recorder that
// handlers use (read-only).
type ActivityService interface {
	ListByTask(ctx context.Context, taskID string) ([]*activity.Activity, error)
	ListByActor(ctx context.Context, actorType activity.ActorType, actorID string) ([]*activity.Activity, error)
	// ListByProject aggregates activity rows from every task in a
	// project, newest first. The limit is clamped at the repository
	// layer (200 default, 500 maximum).
	ListByProject(ctx context.Context, projectID string, limit int) ([]*activity.ProjectActivityEvent, error)
}

// TaskActivityService is the read surface for the task "context"
// endpoint — full snapshot an agent needs to resume work.
type TaskActivityService interface {
	Context(ctx context.Context, taskID string) (*TaskContext, error)
}

// TaskContext is the snapshot returned by GET /api/v1/tasks/:id/context.
//
// Defined here (rather than in internal/domain) so the API layer stays
// the single source of truth for the wire shape.
type TaskContext struct {
	Task     *task.Task           `json:"task"`
	Comments []*comment.Comment   `json:"comments"`
	Activity []*activity.Activity `json:"activity"`
	Subtasks []task.Subtask       `json:"subtasks"`
}
