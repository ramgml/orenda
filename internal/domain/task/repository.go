package task

import "context"

// Filter narrows ListByProject results.
//
// Zero value lists everything in the project.
type Filter struct {
	ProjectID    string
	ColumnID     string
	Status       Status
	AssigneeType AssigneeType
	AssigneeID   string
}

// Repository persists and retrieves Tasks, Subtasks, Checklists and Tags.
//
// Phase 1 implements the minimum CRUD; Phase 2 adds Move() with fractional
// positioning; Phase 3 adds atomic Claim/Release/Submit/Review.
type Repository interface {
	// Create inserts t and returns it with CreatedAt/UpdatedAt populated.
	Create(ctx context.Context, t *Task) error

	// GetByID returns the task with the given id or ErrNotFound.
	GetByID(ctx context.Context, id string) (*Task, error)

	// ListByProject returns tasks matching f, ordered by column position.
	ListByProject(ctx context.Context, f Filter) ([]*Task, error)

	// Update saves changes to an existing task.
	Update(ctx context.Context, t *Task) error

	// Delete removes the task (cascading subtasks/checklists/tags).
	Delete(ctx context.Context, id string) error

	// AddSubtask appends a subtask to the task.
	AddSubtask(ctx context.Context, s *Subtask) error

	// ListSubtasks returns subtasks for the task ordered by position.
	ListSubtasks(ctx context.Context, taskID string) ([]*Subtask, error)

	// UpdateSubtask persists changes to a subtask.
	UpdateSubtask(ctx context.Context, s *Subtask) error

	// DeleteSubtask removes a subtask.
	DeleteSubtask(ctx context.Context, id string) error
}
