// Package checklist holds the Checklist + ChecklistItem entities and
// the small repository surface that backs them.
package checklist

import "context"

// Checklist is a named group of checkable items attached to a task
// (e.g. "Onboarding steps" for a new-hire task). Each task can have
// any number of checklists; items within a checklist are ordered
// by Position.
type Checklist struct {
	ID       string `json:"id"`
	TaskID   string `json:"task_id"`
	Title    string `json:"title"`
	Position int    `json:"position"`
}

// Item is one checkbox row inside a Checklist. Done tracks whether
// the row has been ticked off.
type Item struct {
	ID          string `json:"id"`
	ChecklistID string `json:"checklist_id"`
	Title       string `json:"title"`
	Done        bool   `json:"done"`
	Position    int    `json:"position"`
}

// Repository is the storage surface needed by the checklist service.
// It is intentionally narrow: callers (handlers) do all the
// validation, so the repo can stay SQL-only.
type Repository interface {
	AddList(ctx context.Context, taskID, title string) (*Checklist, error)
	ListLists(ctx context.Context, taskID string) ([]*Checklist, error)
	DeleteList(ctx context.Context, listID string) error

	AddItem(ctx context.Context, listID, title string) (*Item, error)
	ListItems(ctx context.Context, listID string) ([]*Item, error)
	// UpdateItem supports partial updates — pass nil for the
	// fields that should not change.
	UpdateItem(ctx context.Context, itemID string, done *bool, title *string) error
	DeleteItem(ctx context.Context, itemID string) error
}
