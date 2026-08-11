package task

import (
	"context"
	"time"
)

// Filter narrows ListByProject results.
//
// Zero value lists everything in the project.
//
// ParentTaskID is a tri-state pointer: nil = no filter, &"" = top-level
// tasks only (parent_task_id IS NULL), &"abc" = direct children of
// task "abc". Used by the kanban (default = top-level) and by
// GET /tasks/:id/children (ParentTaskID=&parentID).
type Filter struct {
	ProjectID    string
	ColumnID     string
	Status       Status
	AssigneeType AssigneeType
	AssigneeID   string
	ParentTaskID *string
}

// Repository persists and retrieves Tasks, Checklists and Tags.
//
// Phase 1 implements the minimum CRUD; Phase 2 adds Move() with fractional
// positioning; Phase 3 adds atomic Claim/Release/Submit/Review; Phase 11
// folds the legacy events table into tasks (calendar fields below);
// Phase 14 promotes the legacy "subtasks" entity to first-class child
// tasks via parent_task_id.
type Repository interface {
	// Create inserts t. Returns it with CreatedAt/UpdatedAt populated.
	Create(ctx context.Context, t *Task) error

	// GetByID returns the task with the given id or ErrNotFound.
	GetByID(ctx context.Context, id string) (*Task, error)

	// ListByProject returns tasks matching f, ordered by column position.
	ListByProject(ctx context.Context, f Filter) ([]*Task, error)

	// ListChildren returns every direct child task of parentID (tasks
	// where parent_task_id = parentID), ordered by position then
	// creation time. Returns an empty slice when the parent has no
	// children; returns ErrNotFound when the parent itself doesn't
	// exist (so callers can distinguish "no children" from
	// "bad parent").
	ListChildren(ctx context.Context, parentID string) ([]*Task, error)

	// ChildProgress returns the total child count and the count of
	// children whose status is `done`. Used to render the parent
	// task's progress bar.
	ChildProgress(ctx context.Context, parentID string) (total, done int, err error)

	// ListInRange returns every timed task (StartAt/EndAt set) whose
	// interval overlaps [from, to]. Used by the calendar view. An empty
	// projectID means "every project the caller has access to".
	ListInRange(ctx context.Context, from, to time.Time, projectID string) ([]*Task, error)

	// Update saves changes to an existing task.
	Update(ctx context.Context, t *Task) error

	// Delete removes the task. ON DELETE CASCADE on parent_task_id
	// makes sure children disappear with the parent.
	Delete(ctx context.Context, id string) error

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

	AddChecklist(ctx context.Context, taskID, title string) (row *ChecklistRow, err error)
	ListChecklists(ctx context.Context, taskID string) (rows []ChecklistRow, err error)
	DeleteChecklist(ctx context.Context, listID string) error

	AddChecklistItem(ctx context.Context, listID, title string) (row *ChecklistItemRow, err error)
	ListChecklistItems(ctx context.Context, listID string) (rows []ChecklistItemRow, err error)
	UpdateChecklistItem(ctx context.Context, itemID string, done *bool, title *string) error
	DeleteChecklistItem(ctx context.Context, itemID string) error
}

// ChecklistRow + ChecklistItemRow are flat DTOs surfaced through
// the Repository so handlers don't need to import the checklist
// package just to read rows. JSON tags follow the snake_case
// convention used by the rest of the API.
type ChecklistRow struct {
	ID       string `json:"id"`
	TaskID   string `json:"task_id"`
	Title    string `json:"title"`
	Position int    `json:"position"`
}

type ChecklistItemRow struct {
	ID          string `json:"id"`
	ChecklistID string `json:"checklist_id"`
	Title       string `json:"title"`
	Done        bool   `json:"done"`
	Position    int    `json:"position"`
}
