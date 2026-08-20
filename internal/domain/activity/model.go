// Package activity holds the TaskActivity domain entity — an append-only
// audit log of every action performed on a task.
//
// Phase 3.9 (Activity service) writes here for Claim, Release, Submit,
// Review, status changes, comment additions, attachment uploads, and Move
// (already wired in Phase 2.4).
package activity

import (
	"errors"
	"time"
)

// ActorType identifies who performed the action.
type ActorType string

const (
	ActorUser   ActorType = "user"
	ActorAgent  ActorType = "agent"
	ActorSystem ActorType = "system"
)

// Action enumerates known activity kinds. Stored as a string so a future
// action lands without a schema change.
type Action string

const (
	ActionCreated       Action = "task.created"
	ActionClaimed       Action = "task.claimed"
	ActionReleased      Action = "task.released"
	ActionSubmitted     Action = "task.submitted"
	ActionReviewed      Action = "task.reviewed"
	ActionMoved         Action = "task.moved"
	ActionCommented     Action = "task.commented"
	ActionAttachmentAdd Action = "task.attachment_added"
	// Phase 27.9: status/priority/assignee changes adopt the
	// `task.*` prefix so the frontend verb map renders them as
	// human strings. Pre-27.9 rows stored these without the prefix;
	// TaskViewBody's verb map includes both spellings so old data
	// still renders.
	ActionStatusChanged   Action = "task.status_changed"
	ActionPriorityChanged Action = "task.priority_changed"
	ActionAssigned        Action = "task.assigned"

	// Phase 14 additions for child tasks + checklists. The names
	// keep the task.* prefix in the public activity feed (see
	// TaskViewBody's verb map) and make grep'ing activity rows
	// straightforward.
	ActionChildAdded         Action = "child_added"
	ActionChildStatusChanged Action = "child_status_changed"
	ActionChecklistAdded     Action = "checklist_added"
	ActionChecklistItemAdded Action = "checklist_item_added"
	ActionChecklistItemDone  Action = "checklist_item_done"

	// Phase 13: tags + colour label. Tags are a single replace-style
	// verb (rather than add/remove) because the UI sends the full set
	// and the diff doesn't add signal. Payload: {"before": [name,…],
	// "after": [name,…]}. Colour: {"from": "", "to": "#abcdef"}; the
	// "" before/after sentinel means "no colour".
	ActionTagsReplaced Action = "tags_replaced"
	ActionColorChanged Action = "color_changed"

	// Phase 33.2: agent-side task management audit verbs.
	// task.updated covers proposal edits and agent_notes writes
	// (both are field-level mutations); the payload distinguishes
	// the two cases (per-field diff vs. notes before/after).
	// task.deleted covers hard delete of an agent's proposal; the
	// payload is a snapshot of the task so the activity row stays
	// meaningful after the row is gone.
	ActionUpdated    Action = "task.updated"
	ActionDeleted    Action = "task.deleted"
	ActionAgentNotes Action = "task.agent_notes_updated"
)

// Sentinel errors.
var (
	ErrNotFound     = errors.New("activity: not found")
	ErrInvalidInput = errors.New("activity: invalid input")
)

// Activity is one row in task_activity.
type Activity struct {
	ID        string    `json:"id"`
	TaskID    string    `json:"task_id"`
	ActorType ActorType `json:"actor_type"`
	ActorID   string    `json:"actor_id"`
	Action    Action    `json:"action"`
	Payload   string    `json:"payload,omitempty"` // free-form JSON
	CreatedAt time.Time `json:"created_at"`
}

// Validate returns an error if the Activity fields are inconsistent.
func (a *Activity) Validate() error {
	if a.TaskID == "" || a.ActorID == "" || a.Action == "" {
		return ErrInvalidInput
	}
	if a.ActorType == "" {
		a.ActorType = ActorUser
	}
	return nil
}
