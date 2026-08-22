// Package task holds the Task domain entity and related value objects.
//
// Phase 1 implements the minimum surface required by docs/PRD.md F-T-1..F-T-9:
// CRUD, subtasks, checklists, tags, statuses, priority, assignee, awaiting.
// Phase 3 will add atomic claim/release and review flow.
package task

import (
	"errors"
	"regexp"
	"time"
)

// Status enumerates the lifecycle stages of a task.
//
// The PRD (S4) requires backlog → todo → in_progress → review → done with a
// rejected → todo loop. The application layer enforces valid transitions; the
// repository only stores the string.
type Status string

const (
	StatusBacklog    Status = "backlog"
	StatusTodo       Status = "todo"
	StatusInProgress Status = "in_progress"
	StatusReview     Status = "review"
	StatusDone       Status = "done"
)

// AllStatuses is the ordered list of statuses (matches DefaultColumns order).
var AllStatuses = []Status{
	StatusBacklog, StatusTodo, StatusInProgress, StatusReview, StatusDone,
}

// StatusMachineKeyPattern is the regex that a machine key (column.status,
// task.status set via axis collapse) must satisfy. Phase 27.8 made
// columns carry their own machine key so projects can ship custom
// statuses; this keeps the surface safe (no spaces, no quotes, no path
// injection across the markdown mirror or WS payload).
var StatusMachineKeyPattern = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// IsValid reports whether s is a usable status. Phase 27.8.4: the
// canonical five (AllStatuses) are still accepted — agents that hard-
// code them keep working — but a project may also define its own
// status machine keys via column.status. As long as the value is a
// well-formed machine key we trust the column invariant `task.status
// ≡ column.status` enforced at the service layer.
//
// Task.Validate uses this; column-input validation lives next to the
// column handlers (StatusMachineKeyPattern is reused there).
func (s Status) IsValid() bool {
	if s == "" {
		return false
	}
	if _, ok := CanonicalStatus(string(s)); ok {
		return true
	}
	return StatusMachineKeyPattern.MatchString(string(s))
}

// IsCanonical reports whether s is one of the five default statuses.
// Code that needs to render an enum-style dropdown (instead of the
// project board's columns) uses this; everywhere else IsValid is the
// right choice.
func (s Status) IsCanonical() bool {
	_, ok := CanonicalStatus(string(s))
	return ok
}

// CanonicalStatus looks up a default status by its string value.
// Used by IsValid/IsCanonical to keep the canonical set authoritative
// in exactly one place.
func CanonicalStatus(v string) (Status, bool) {
	switch Status(v) {
	case StatusBacklog, StatusTodo, StatusInProgress, StatusReview, StatusDone:
		return Status(v), true
	default:
		return "", false
	}
}

// Priority enumerates task priority levels.
type Priority string

const (
	PriorityLow    Priority = "low"
	PriorityMedium Priority = "medium"
	PriorityHigh   Priority = "high"
	PriorityUrgent Priority = "urgent"
)

// Awaiting enumerates who must act next on a task.
type Awaiting string

const (
	AwaitingNone  Awaiting = "none"
	AwaitingHuman Awaiting = "human"
	AwaitingAgent Awaiting = "agent"
)

// AssigneeType identifies the kind of actor assigned to a task.
//
// The actual actor ID lives in AssigneeID; the two columns are read together.
// Storing the type avoids needing a polymorphic FK and keeps queries indexable.
type AssigneeType string

const (
	AssigneeUser  AssigneeType = "user"
	AssigneeAgent AssigneeType = "agent"
)

// CreatorType identifies the kind of actor that originally filed the
// task. The shape is the same as AssigneeType (a discriminator + a
// nullable id), but the semantics are different: CreatedBy is set
// once at insert and never changes. The agent's PATCH/DELETE
// permission gate (Phase 33.2) reads `created_by_type='agent' AND
// created_by_id=me` to decide whether a no-longer-backlog proposal
// is editable.
type CreatorType string

const (
	CreatorUser  CreatorType = "user"
	CreatorAgent CreatorType = "agent"
)

// Sentinel errors returned by task repository implementations.
var (
	ErrNotFound         = errors.New("task: not found")
	ErrInvalidInput     = errors.New("task: invalid input")
	ErrSelfDependency   = errors.New("task: task cannot depend on itself")
	ErrDependencyCycle  = errors.New("task: dependency cycle")
	ErrDependencyExists = errors.New("task: dependency already exists")
)

// Task is the canonical task entity.
//
// Position is a float to support fractional insertion in Phase 2 kanban
// (place between two siblings by averaging their positions). CreatedAt and
// UpdatedAt are managed by the storage layer.
//
// Tasks with a non-nil StartAt + EndAt show on the calendar view (Phase 11
// unified the legacy events table into tasks). A task can be on the
// calendar, on the kanban, or both.
type Task struct {
	ID string `json:"id"`
	// Number is the human-readable sequential id ("T42"). Assigned by
	// the storage layer on Create from the task_number_seq high-
	// watermark; never reused after a delete. The agent surface (REST
	// /agent/tasks/{id}/*, CLI, MCP) resolves "T42" through this
	// column — see ParseRefNumber and Repository.GetByNumber.
	Number       int          `json:"number"`
	ProjectID    string       `json:"project_id"`
	ParentTaskID string       `json:"parent_task_id,omitempty"`
	ColumnID     string       `json:"column_id,omitempty"`
	Title        string       `json:"title"`
	Description  string       `json:"description,omitempty"`
	Status       Status       `json:"status"`
	Priority     Priority     `json:"priority"`
	AssigneeType AssigneeType `json:"assignee_type,omitempty"`
	AssigneeID   string       `json:"assignee_id,omitempty"`
	// Phase 33.2: who filed the task. Set at insert by the
	// user-side POST /api/v1/tasks (CreatorUser) and the agent-side
	// POST /api/v1/agent/tasks (CreatorAgent). The id is nullable
	// for legacy rows that predate migration 024; the agent-side
	// edit gate treats NULL id as "not my row" so an agent can't
	// piggy-back on a legacy proposal.
	CreatedByType CreatorType `json:"created_by_type,omitempty"`
	CreatedByID   string      `json:"created_by_id,omitempty"`
	Awaiting      Awaiting    `json:"awaiting"`
	ContextMD     string      `json:"context_md,omitempty"`
	AgentNotes    string      `json:"agent_notes,omitempty"`
	DueAt         *time.Time  `json:"due_at,omitempty"`
	StartedAt     *time.Time  `json:"started_at,omitempty"`
	ClaimedAt     *time.Time  `json:"claimed_at,omitempty"`
	CompletedAt   *time.Time  `json:"completed_at,omitempty"`
	TimeEstimateS *int        `json:"time_estimate_s,omitempty"`
	TimeSpentS    int         `json:"time_spent_s"`
	Position      float64     `json:"position"`

	// Calendar fields. When both StartAt and EndAt are set the task
	// shows on the calendar; otherwise it's a plain kanban item.
	StartAt *time.Time `json:"start_at,omitempty"`
	EndAt   *time.Time `json:"end_at,omitempty"`
	AllDay  bool       `json:"all_day"`
	// Color is intentionally NOT omitempty: an empty string carries
	// meaning ("no colour label"), and clients need to see the
	// distinction between "absent" and "explicit empty" after a
	// PATCH /tasks/{id} {color: ""}.
	Color string `json:"color"`
	// Phase 23.3: RFC 5545 RRULE. Migration 015 added the column
	// to the tasks table (Phase 12's fold dropped it). The calendar
	// expands this via Service.ExpandRecurrence; "" means no
	// recurrence and the master renders as a single occurrence.
	Recurrence string `json:"recurrence,omitempty"`

	// Phase 17: aggregate counters populated by the list endpoint
	// so the kanban card can render "comments / attachments /
	// progress" without a per-card fetch. Always optional — GET
	// /tasks/{id} doesn't set them; the handlers that wrap
	// ListByProject do. The card UI treats absent values as "0".
	Counters *Counters `json:"counters,omitempty"`

	// Phase 15: number of unfinished blockers. Same optional
	// lifecycle — only set by list endpoints that join with
	// task_dependencies. The card uses it to render the "blocked"
	// badge; absent ⇒ not blocked.
	BlockedByCount int `json:"blocked_by_count,omitempty"`

	// Phase 27.3: tags attached to this task. Populated by the list
	// endpoints (single batch query in ListByProjectWithStats via
	// TagsForTasks), and on GET /tasks/{id} for symmetry. Always
	// nil/empty for tasks with no tags; the omitempty keeps single-
	// task JSON responses compact and saves every list consumer from
	// having to default an empty array.
	Tags []Tag `json:"tags,omitempty"`

	// Phase 31: study reminder marker. Non-empty ⇔ this task is a
	// soft "study today" nudge created from a study_proposal that
	// the user accepted. The link is loose — deleting the course
	// clears this id (the reminder itself survives) and there is no
	// referential link to specific lessons/quizzes. Today reads
	// this to decide whether to surface the task under "due_today"
	// (always, when due_at <= today) vs. escalate missed days into
	// "overdue" (never). omitempty: a task without the link stays
	// a plain task in the JSON payload.
	StudyCourseID string `json:"study_course_id,omitempty"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Counters is the bundle of per-task counts the kanban card
// renders. Phase 17 aggregates them in a single SQL query so the
// card render doesn't fan out into per-row fetches.
type Counters struct {
	Comments       int `json:"comments"`
	Attachments    int `json:"attachments"`
	ChildrenTotal  int `json:"children_total"`
	ChildrenDone   int `json:"children_done"`
	ChecklistTotal int `json:"checklist_total"`
	ChecklistDone  int `json:"checklist_done"`
}

// Validate returns an error if the Task fields are inconsistent.
//
// ProjectID is allowed to be empty: an "Inbox" task (Phase 16) has no
// project — it floats until the user files it under one. The invariant
// is then that ColumnID must also be empty (an inbox card has no
// board / column to live in).
func (t *Task) Validate() error {
	if t.Title == "" {
		return ErrInvalidInput
	}
	// Phase 16: empty ProjectID is the Inbox case. The pair rule
	// "project set ⇒ column optional" stays, but "inbox ⇒ no column"
	// is now an explicit invariant.
	if t.ProjectID == "" && t.ColumnID != "" {
		return ErrInvalidInput
	}
	if t.Status == "" {
		t.Status = StatusTodo
	}
	if !t.Status.IsValid() {
		return ErrInvalidInput
	}
	if t.Priority == "" {
		t.Priority = PriorityMedium
	}
	if t.Awaiting == "" {
		t.Awaiting = AwaitingNone
	}
	// Calendar fields: if one is set, both must be set. End must be
	// strictly after start.
	if (t.StartAt == nil) != (t.EndAt == nil) {
		return ErrInvalidInput
	}
	if t.StartAt != nil && t.EndAt != nil && !t.EndAt.After(*t.StartAt) {
		return ErrInvalidInput
	}
	return nil
}

// Checklist groups ChecklistItems under a task.
type Checklist struct {
	ID       string          `json:"id"`
	TaskID   string          `json:"task_id"`
	Title    string          `json:"title"`
	Position int             `json:"position"`
	Items    []ChecklistItem `json:"items,omitempty"`
}

// ChecklistItem is one entry in a Checklist.
type ChecklistItem struct {
	ID          string `json:"id"`
	ChecklistID string `json:"checklist_id"`
	Title       string `json:"title"`
	Done        bool   `json:"done"`
	Position    int    `json:"position"`
}

// Tag is a free-form label that can be attached to tasks (Phase 1
// schema; Phase 13 wires the API + UI).
//
// A tag is identified by its unique name; the colour is purely cosmetic
// (rendered as the chip background on cards and the task sidebar).
type Tag struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Color string `json:"color,omitempty"`
}

// Validate returns an error if the Tag is inconsistent.
//
// Name rules: non-empty, <= 50 chars. Colour is optional; when present
// it must look like a CSS hex colour (#RGB or #RRGGBB) — the UI uses
// <input type="color"> which already enforces this, but the check
// here protects against a hand-crafted API client.
func (t *Tag) Validate() error {
	if t.Name == "" {
		return ErrInvalidInput
	}
	if len(t.Name) > 50 {
		return ErrInvalidInput
	}
	if t.Color != "" && !isHexColor(t.Color) {
		return ErrInvalidInput
	}
	return nil
}

// isHexColor reports whether s looks like "#abc" or "#aabbcc" (CSS hex).
func isHexColor(s string) bool {
	if len(s) != 4 && len(s) != 7 {
		return false
	}
	if s[0] != '#' {
		return false
	}
	for i := 1; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= '0' && c <= '9':
		case c >= 'a' && c <= 'f':
		case c >= 'A' && c <= 'F':
		default:
			return false
		}
	}
	return true
}
