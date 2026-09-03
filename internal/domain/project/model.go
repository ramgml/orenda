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
	ErrNotFound       = errors.New("project: not found")
	ErrInvalidInput   = errors.New("project: invalid input")
	ErrColumnNotEmpty = errors.New("project: column has tasks")
)

// Project is the canonical project entity.
//
// Soft-deleted via Archived=true; Phase 9+ will add a UI toggle for the
// archive view but Phase 1 only exposes Create/List/Get/Update/Delete.
//
// AgentsAllowed (task 140) is the per-project agent-access switch:
// false = closed, only agents with an explicit grant row in
// project_agents may see/claim its tasks (an empty grant list means
// nobody); true = open to every agent.
//
// WikiSlug (wiki:project-wiki-link) points at the wiki page that holds
// the project's documentation (постановка, decision log, etc.). Empty
// means no link. The DB layer treats empty string and SQL NULL as the
// same state — handlers normalize "" to NULL on write and the repo
// returns NULL as "" on read. The FK is wiki_pages.slug ON DELETE SET
// NULL, so deleting a wiki page silently unlinks the project (which is
// the right outcome — the page is gone, the project still exists).
type Project struct {
	ID            string    `json:"id"`
	Number        int       `json:"number"`
	Name          string    `json:"name"`
	Color         string    `json:"color"`
	Description   string    `json:"description,omitempty"`
	WikiSlug      string    `json:"wiki_slug,omitempty"`
	OwnerID       string    `json:"owner_id"`
	Archived      bool      `json:"archived"`
	AgentsAllowed bool      `json:"agents_allowed"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
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
//
// Status is the Phase 27.8 machine key that links the column to a
// `task.Status` value. Default columns have Status == Name (lowercase
// form: "backlog", "todo", "in_progress", "review", "done"). Custom
// columns get a slugified status — see migration 020 for the rules.
// The service layer treats the pair (task.status, column.status) as a
// single invariant: status(column_id) == task.status, and
// status-equivalent columns are interchangeable within a board.
type Column struct {
	ID      string `json:"id"`
	BoardID string `json:"board_id"`
	// ProjectID is populated by the storage layer when columns are
	// read through GetColumn (it joins against boards). Phase 16
	// needs it so the kanban can file an Inbox card under the
	// project of the column it was dropped onto without a second
	// round-trip.
	ProjectID string  `json:"project_id,omitempty"`
	Name      string  `json:"name"`
	Position  float64 `json:"position"`
	WIPLimit  *int    `json:"wip_limit,omitempty"`
	Color     string  `json:"color,omitempty"`
	// Status is the machine key, populated by the storage layer.
	// Empty for pre-27.8 boards that haven't run migration 020 yet;
	// the service layer falls back to the default status set in
	// that case (see task.Service.lookupColumnForStatus).
	Status string `json:"status"`
}

// DefaultColumns lists the four columns every new project starts with.
//
// Phase 2 will switch this to a configurable list owned by the user; for
// Phase 1 we ship a fixed sequence that matches the kanban workflow from
// docs/PRD.md (S4).
var DefaultColumns = []string{"backlog", "todo", "in_progress", "review", "done"}
