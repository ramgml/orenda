package task

import (
	"context"
	"time"
)

// Filter narrows ListByProject results.
//
// Zero value lists inbox tasks (tasks with project_id IS NULL).
// The Filter models three orthogonal axes:
//
//   - "which project?" — ProjectID for normal lookup; NoProject=true
//     for the inbox (mutually exclusive with ProjectID).
//   - "top-level vs children" — ParentTaskID tri-state pointer; nil
//     = no filter, &"" = top-level (parent_task_id IS NULL),
//     &"abc" = direct children of task "abc".
//   - "which column / assignee / status" — straightforward equality.
//
// The kanban list endpoint uses ProjectID; the inbox list endpoint
// uses NoProject; /tasks/:id/children uses ParentTaskID=&parentID.
// Service.Move calls ListByProject with only ColumnID set so it can
// count tasks in a target column regardless of which project they
// belong to (Phase 16.4 unblocked this).
type Filter struct {
	ProjectID    string
	NoProject    bool // Phase 16: true → "WHERE project_id IS NULL"
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

	// ---- Tags (Phase 13) ----
	//
	// Tags are global (no project_id) — they're a label vocabulary
	// shared across all the user's projects. Phase 13 wires CRUD on
	// the catalogue and assignment to tasks; per-tag scoping to a
	// project is a Phase 18+ concern if it ever becomes one.
	//
	// SetTaskTags replaces the task's tag set atomically (single tx:
	// DELETE then INSERT). The handler is responsible for skipping
	// this call when the request carries the same set as the current
	// one to avoid spurious activity entries.
	//
	// TagsForTasks is the batch shape used by the kanban list
	// endpoint: one query returns {taskID: []*Tag} for every task in
	// the input slice, so per-card fetch on the frontend isn't
	// needed. Order: tag name ASC.
	ListTags(ctx context.Context) ([]Tag, error)
	GetTagByID(ctx context.Context, id string) (*Tag, error)
	CreateTag(ctx context.Context, t *Tag) error
	UpdateTag(ctx context.Context, t *Tag) error
	DeleteTag(ctx context.Context, id string) error
	ListTagsForTask(ctx context.Context, taskID string) ([]Tag, error)
	SetTaskTags(ctx context.Context, taskID string, tagIDs []string) error
	TagsForTasks(ctx context.Context, taskIDs []string) (map[string][]Tag, error)
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
