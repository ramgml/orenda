// Package project holds the Project, Board and Column domain entities.
//
// The package is deliberately tiny: entities are plain Go structs with no
// SQL/JSON tags (those are presentation concerns handled by the storage and
// API layers). Repository contracts live in repository.go in the same
// package.
package project

import (
	"errors"
	"time"
)

// Sentinel errors returned by project repository implementations.
var (
	ErrNotFound     = errors.New("project: not found")
	ErrInvalidInput = errors.New("project: invalid input")
)

// Project is the canonical project entity.
//
// Soft-deleted via Archived=true; Phase 9+ will add a UI toggle for the
// archive view but Phase 1 only exposes Create/List/Get/Update/Delete.
type Project struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Color       string    `json:"color"`
	Description string    `json:"description,omitempty"`
	OwnerID     string    `json:"owner_id"`
	Archived    bool      `json:"archived"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// DefaultColor is the color used when a project is created without one
// and when an explicit empty color is PATCHed in (the latter meaning
// "reset to default").
const DefaultColor = "#3b82f6"

// Validate returns an error if the Project fields are inconsistent.
func (p *Project) Validate() error {
	if p.Name == "" {
		return ErrInvalidInput
	}
	if p.OwnerID == "" {
		return ErrInvalidInput
	}
	if p.Color == "" {
		p.Color = DefaultColor
	}
	return nil
}

// Board groups Columns under a Project. Phase 2 will introduce default boards
// at project creation; for Phase 1 boards are created implicitly with one
// board per project and the four default columns below.
type Board struct {
	ID        string    `json:"id"`
	ProjectID string    `json:"project_id"`
	Name      string    `json:"name"`
	Position  int       `json:"position"`
	CreatedAt time.Time `json:"created_at"`
}

// Column is a kanban column belonging to a Board.
//
// Position is a float so drag-and-drop in Phase 2 can place a task between
// two siblings without renumbering all subsequent positions (just pick
// (prev + next) / 2).
type Column struct {
	ID       string  `json:"id"`
	BoardID  string  `json:"board_id"`
	Name     string  `json:"name"`
	Position float64 `json:"position"`
	WIPLimit *int    `json:"wip_limit,omitempty"`
	Color    string  `json:"color,omitempty"`
}

// DefaultColumns lists the four columns every new project starts with.
//
// Phase 2 will switch this to a configurable list owned by the user; for
// Phase 1 we ship a fixed sequence that matches the kanban workflow from
// docs/PRD.md (S4).
var DefaultColumns = []string{"backlog", "todo", "in_progress", "review", "done"}
