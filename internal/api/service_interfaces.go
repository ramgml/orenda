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
	// ListByProject returns every attachment in the project (project +
	// task attachments) with the task title annotated on each row.
	ListByProject(ctx context.Context, projectID string) ([]*attachment.ProjectAttachment, error)
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

// ActivityRecorder is the write surface for activity rows that
// handlers fire as a side-effect of their primary mutation. It is
// intentionally narrower than the full Recorder type in
// internal/service/activity so the api package doesn't depend on
// that package (the service layer builds a small adapter in
// cmd/orenda). Nil-safe in handlers — a missing recorder must NOT
// fail the user-facing request, just silently skip the side-effect
// (mirrors the long-standing convention for deps.Notifier).
//
// Phase 28.5: introduced so the comment + attachment handlers can
// finally emit `task.commented` and `task.attachment_added`. The
// constants exist in `internal/domain/activity` since Phase 6 but
// nothing wrote them until this phase.
type ActivityRecorder interface {
	RecordTask(
		ctx context.Context,
		taskID string,
		actorType activity.ActorType,
		actorID string,
		action activity.Action,
		payload string,
	) error
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
//
// Phase 14: Subtasks became full child tasks via parent_task_id, so
// the snapshot now carries `Children`. Checklists were previously
// invisible to agents; we expose both lists and items so an agent
// resuming work sees the same structure the human does.
type TaskContext struct {
	Task           *task.Task                         `json:"task"`
	Comments       []*comment.Comment                 `json:"comments"`
	Activity       []*activity.Activity               `json:"activity"`
	Children       []*task.Task                       `json:"children"`
	Checklists     []task.ChecklistRow                `json:"checklists"`
	ChecklistItems map[string][]task.ChecklistItemRow `json:"checklist_items"`
}
