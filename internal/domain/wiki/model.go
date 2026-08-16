// Package wiki holds the WikiPage domain entity and repository interface.
package wiki

import (
	"errors"
	"time"
)

// Sentinel errors.
var (
	ErrNotFound     = errors.New("wiki: not found")
	ErrInvalidInput = errors.New("wiki: invalid input")
	ErrSlugTaken    = errors.New("wiki: slug already in use")
)

// Page is a wiki page.
type Page struct {
	ID        string    `json:"id"`
	ParentID  string    `json:"parent_id,omitempty"`
	Slug      string    `json:"slug"`
	Title     string    `json:"title"`
	ContentMD string    `json:"content_md"`
	Position  int       `json:"position"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Validate enforces invariants.
func (p *Page) Validate() error {
	if p.Slug == "" || p.Title == "" {
		return ErrInvalidInput
	}
	return nil
}

// Link records a [[slug]] → page_id edge. Stored as (from_page_id, to_page_id)
// in wiki_links so backlinks are just a reverse lookup.
type Link struct {
	FromPageID string `json:"from_page_id"`
	ToPageID   string `json:"to_page_id"`
}

// TreeNode is a node in the hierarchical tree view.
type TreeNode struct {
	Page     *Page       `json:"page"`
	Children []*TreeNode `json:"children,omitempty"`
}
