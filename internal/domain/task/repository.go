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
	// AssigneeTypeIncludeNull widens AssigneeType to also match rows
	// with no assignee at all (assignee_type IS NULL). Phase 33.1: the
	// agent work surface lists "todo tasks an agent could claim" — an
	// unassigned todo task is claimable by any agent, so the filter
	// must not hide it.
	AssigneeTypeIncludeNull bool
	AssigneeID              string
	ParentTaskID            *string
	// IDs restricts the result to the given task ids (Phase 28.22).
	// Composable with the other clauses; empty = no restriction. Used
	// by the /today handler to enrich only the visible tasks instead
	// of scanning the whole table.
	IDs []string
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

	// GetByNumber returns the task with the given human-readable
	// number (the "#42" reference) or ErrNotFound. Numbers are
	// assigned sequentially at Create and never reused.
	GetByNumber(ctx context.Context, number int) (*Task, error)

	// ListByProject returns tasks matching f, ordered by column position.
	ListByProject(ctx context.Context, f Filter) ([]*Task, error)

	// ListByProjectWithStats is ListByProject plus per-task counters
	// (Phase 17). The Counters and BlockedByCount fields are populated
	// on each Task so the kanban card can render "comments / progress /
	// blocked" without a per-card fetch.
	//
	// Used by /projects/{id}/board and /inbox/tasks list endpoints.
	// The single-task endpoints (GET /tasks/{id}) keep using
	// ListByProject + a separate follow-up call.
	ListByProjectWithStats(ctx context.Context, f Filter) ([]*Task, error)

	// ListByDueBetween returns tasks whose due_at falls within
	// [from, to]. Phase 30.8: the calendar needs to render tasks
	// alongside timed events. Tasks without a due_at are not
	// returned. Result is unordered — the caller sorts (the
	// calendar UI groups by date anyway).
	ListByDueBetween(ctx context.Context, from, to time.Time) ([]*Task, error)

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

	// UpdateProposalFields is the gate-protected partial UPDATE for
	// the agent-side EditProposal flow (Phase 33.2.1). It writes
	// ONLY the patch fields and asserts the (created_by_type,
	// created_by_id, status) gate directly in the WHERE clause —
	// a concurrent owner triage that flips status out of 'backlog'
	// makes RowsAffected()==0 and the caller surfaces
	// ErrConcurrentTriage. This is the TOCTOU fix for the
	// pre-33.2.1 GetByID → Update path.
	UpdateProposalFields(ctx context.Context, params ProposalPatchParams) error

	// UpdateAgentNotesField is the gate-protected partial UPDATE
	// for the agent-side UpdateAgentNotes flow (Phase 33.2.1).
	// Writes ONLY agent_notes and asserts the holder gate
	// (assignee_type='agent' AND assignee_id=?) directly in the
	// WHERE clause. A concurrent Release that clears the
	// assignee makes RowsAffected()==0.
	UpdateAgentNotesField(ctx context.Context, taskID, agentID, notes string) error

	// DeleteWithProposalGate is the gate-protected DELETE for the
	// retract flow (Phase 33.2.1). Same gate as
	// UpdateProposalFields. The tombstone row is written BEFORE
	// this returns (see service.RetractProposal).
	DeleteWithProposalGate(ctx context.Context, taskID, agentID string) error

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

	// ---- Task dependencies (Phase 15) ----
	//
	// A "dependency" means "this task can't be claimed until the
	// listed ones are done". Blockers are queried from the "depends
	// on" side (deps of task X = blockers of X); dependents is the
	// reverse lookup, useful when finishing a task to know what just
	// got unblocked.
	//
	// AddDependency returns ErrDependencyCycle when adding the edge
	// would create a cycle in the dependency graph (A→B plus B→A);
	// ErrSelfDependency when task_id == depends_on_task_id. The
	// service does a DFS walk to enforce cycle-freeness before the
	// repository inserts.
	AddDependency(ctx context.Context, taskID, dependsOnID string) error
	// RemoveDependency deletes one edge. Idempotent: deleting a
	// non-existent edge is a no-op (no error).
	RemoveDependency(ctx context.Context, taskID, dependsOnID string) error
	// SetTaskDependencies replaces the task's full blocker set in a
	// single tx (DELETE then INSERT). Empty slice = clear all.
	SetTaskDependencies(ctx context.Context, taskID string, dependsOnIDs []string) error
	// Blockers returns the tasks that block taskID (i.e. tasks that
	// taskID depends on and that are not yet 'done'). Includes the
	// dependency list itself (every edge), the implementor filters
	// by status; we keep this method returning ALL blockers (every
	// edge, regardless of status) and let the caller filter — that
	// way the UI can render "blocked by N (M still open)".
	Blockers(ctx context.Context, taskID string) ([]BlockerRow, error)
	// BlockersForTasks is the batch form of Blockers (Phase 28.22):
	// one round-trip for many task ids, keyed by task id. Every input
	// id gets an entry (possibly an empty slice) so callers can
	// distinguish "no blockers" from "not queried". Used by the agent
	// ready-listing to avoid a per-task N+1.
	BlockersForTasks(ctx context.Context, taskIDs []string) (map[string][]BlockerRow, error)
	// Dependents returns tasks that depend on taskID (reverse lookup).
	Dependents(ctx context.Context, taskID string) ([]string, error)

	// ---- Review queue (Phase 19) ----
	//
	// ListAwaitingReview returns every task awaiting human action,
	// newest-first. "Awaiting" is the union of two signals:
	//
	//   - awaiting='human'   — agent submitted, awaiting owner verdict
	//   - status='review'    — same logical state; we accept both for
	//                          safety (legacy code path may set status
	//                          without setting awaiting, or vice-versa)
	//
	// Inbox tasks (project_id IS NULL after Phase 16) are included;
	// the joined project name + colour come back as empty strings.
	ListAwaitingReview(ctx context.Context) ([]ReviewQueueItem, error)

	// TitlesByIDs returns id→title for every requested task in a
	// single round-trip (Phase 27.9). Missing ids are simply absent
	// from the map; callers should treat "no key" as "task gone",
	// fall back to a slice of the id. Empty input → empty map.
	//
	// Used by the time-entry report to render task titles next to
	// the aggregated seconds.
	TitlesByIDs(ctx context.Context, ids []string) (map[string]string, error)
}

// ProposalPatchParams is the patch shape accepted by
// UpdateProposalFields. Pointer fields use nil to mean "leave
// alone" and a non-nil pointer to mean "set to this value",
// matching the wire shape of PATCH /agent/tasks/{id}.
type ProposalPatchParams struct {
	TaskID      string
	Gate        ProposalGate
	Title       *string
	Description *string
	Priority    *Priority
	DueAt       *time.Time
	ParentID    *string
}

// ProposalGate carries the (created_by_type='agent' AND
// created_by_id=me) gate the manager asserts in the WHERE clause.
type ProposalGate struct {
	CreatedByID string
}

// ReviewQueueItem is a task awaiting review, denormalised with its
// project name + colour so the /review page can render a header line
// without an extra round-trip.
//
// Project fields are empty strings for Inbox tasks (project_id IS NULL).
type ReviewQueueItem struct {
	Task         *Task  `json:"task"`
	ProjectName  string `json:"project_name"`
	ProjectColor string `json:"project_color"`
}

// BlockerRow is one blocker of a task in the dependency graph.
//
// `Done` lets the caller decide whether the blocker still actively
// blocks (Done=false) or has been satisfied (Done=true — the task is
// no longer waiting on this edge). The set returned by Blockers is
// ALL edges regardless of status; filtering is left to the caller so
// the UI can show "blocked by 3 (1 still open)".
type BlockerRow struct {
	BlockerID string `json:"blocker_id"`
	Title     string `json:"title"`
	Status    Status `json:"status"`
	Done      bool   `json:"done"`
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
