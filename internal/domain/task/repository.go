package task

import (
	"context"
	"time"
)

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
// positioning; Phase 3 adds atomic Claim/Release/Submit/Review; Phase 11
// folds the legacy events table into tasks (calendar fields below).
type Repository interface {
	// Create inserts t. Returns it with CreatedAt/UpdatedAt populated.
	Create(ctx context.Context, t *Task) error

	// GetByID returns the task with the given id or ErrNotFound.
	GetByID(ctx context.Context, id string) (*Task, error)

	// ListByProject returns tasks matching f, ordered by column position.
	ListByProject(ctx context.Context, f Filter) ([]*Task, error)

	// ListInRange returns every timed task (StartAt/EndAt set) whose
	// interval overlaps [from, to]. Used by the calendar view. An empty
	// projectID means "every project the caller has access to".
	ListInRange(ctx context.Context, from, to time.Time, projectID string) ([]*Task, error)

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

	// GetSubtask fetches a single subtask by id, or returns ErrNotFound.
	GetSubtask(ctx context.Context, id string) (*Subtask, error)

	// CountByColumn returns how many tasks currently live in the given
	// column (status != 'done' if you only want active ones — Phase 2.8
	// keeps it simple: every task in the column counts toward the limit).
	CountByColumn(ctx context.Context, columnID string) (int, error)

	// FirstColumnID returns the id of the first column of the project's
	// default board (lowest position), or "" if the project has no
	// board. Used by event.Service.Create to put newly-created
	// calendar tasks into a real kanban column.
	FirstColumnID(ctx context.Context, projectID string) (string, error)

	// ---- Checklists (each task can have any number) ----

	AddChecklist(ctx context.Context, taskID, title string) (id string, err error)
	ListChecklists(ctx context.Context, taskID string) (rows []ChecklistRow, err error)
	DeleteChecklist(ctx context.Context, listID string) error
	AddChecklistItem(ctx context.Context, listID, title string) (id string, err error)
	ListChecklistItems(ctx context.Context, listID string) (rows []ChecklistItemRow, err error)
	UpdateChecklistItem(ctx context.Context, itemID string, done *bool, title *string) error
	DeleteChecklistItem(ctx context.Context, itemID string) error
}

// ChecklistRow + ChecklistItemRow are flat DTOs surfaced through
// the Repository so handlers don't need to import the checklist
// package just to read rows.
type ChecklistRow struct {
	ID       string
	TaskID   string
	Title    string
	Position int
}

type ChecklistItemRow struct {
	ID          string
	ChecklistID string
	Title       string
	Done        bool
	Position    int
}
