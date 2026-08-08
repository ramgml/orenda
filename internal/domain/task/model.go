// Package task holds the Task domain entity and related value objects.
//
// Phase 1 implements the minimum surface required by docs/PRD.md F-T-1..F-T-9:
// CRUD, subtasks, checklists, tags, statuses, priority, assignee, awaiting.
// Phase 3 will add atomic claim/release and review flow.
package task

import (
	"errors"
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

// IsValid reports whether s is one of the known statuses.
func (s Status) IsValid() bool {
	for _, v := range AllStatuses {
		if s == v {
			return true
		}
	}
	return false
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

// Sentinel errors returned by task repository implementations.
var (
	ErrNotFound     = errors.New("task: not found")
	ErrInvalidInput = errors.New("task: invalid input")
)

// Task is the canonical task entity.
//
// Position is a float to support fractional insertion in Phase 2 kanban
// (place between two siblings by averaging their positions). CreatedAt and
// UpdatedAt are managed by the storage layer.
type Task struct {
	ID            string       `json:"id"`
	ProjectID     string       `json:"project_id"`
	ParentTaskID  string       `json:"parent_task_id,omitempty"`
	ColumnID      string       `json:"column_id,omitempty"`
	Title         string       `json:"title"`
	Description   string       `json:"description,omitempty"`
	Status        Status       `json:"status"`
	Priority      Priority     `json:"priority"`
	AssigneeType  AssigneeType `json:"assignee_type,omitempty"`
	AssigneeID    string       `json:"assignee_id,omitempty"`
	Awaiting      Awaiting     `json:"awaiting"`
	ContextMD     string       `json:"context_md,omitempty"`
	AgentNotes    string       `json:"agent_notes,omitempty"`
	DueAt         *time.Time   `json:"due_at,omitempty"`
	StartedAt     *time.Time   `json:"started_at,omitempty"`
	ClaimedAt     *time.Time   `json:"claimed_at,omitempty"`
	CompletedAt   *time.Time   `json:"completed_at,omitempty"`
	TimeEstimateS *int         `json:"time_estimate_s,omitempty"`
	TimeSpentS    int          `json:"time_spent_s"`
	Position      float64      `json:"position"`
	CreatedAt     time.Time    `json:"created_at"`
	UpdatedAt     time.Time    `json:"updated_at"`
}

// Validate returns an error if the Task fields are inconsistent.
func (t *Task) Validate() error {
	if t.Title == "" {
		return ErrInvalidInput
	}
	if t.ProjectID == "" {
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
	return nil
}

// Subtask is a simple checkbox under a task (Phase 1 minimum).
type Subtask struct {
	ID       string `json:"id"`
	TaskID   string `json:"task_id"`
	Title    string `json:"title"`
	Done     bool   `json:"done"`
	Position int    `json:"position"`
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

// Tag is a free-form label that can be attached to tasks (Phase 1 minimum).
type Tag struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Color string `json:"color,omitempty"`
}
