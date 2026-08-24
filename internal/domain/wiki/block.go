// Package wiki holds the WikiPage domain entity and repository interface.
package wiki

import (
	"encoding/json"
	"time"
)

// Block is one node of a page's render tree. Props/Content mirror the
// BlockNote block shape ({id, type, props, content, children}) so the
// SPA can round-trip documents without transformation.
type Block struct {
	ID       string          `json:"id"`
	Type     string          `json:"type"`
	Props    json.RawMessage `json:"props,omitempty"`
	Content  json.RawMessage `json:"content,omitempty"`
	Children []*Block        `json:"children,omitempty"`

	PageID        string    `json:"-"`
	ParentBlockID string    `json:"-"`
	Position      int       `json:"-"`
	CreatedAt     time.Time `json:"-"`
	UpdatedAt     time.Time `json:"-"`
}

// BlockTypes is the whitelist of supported block type names (BlockNote
// default block type names). Only types in this list may be persisted.
var BlockTypes = []string{
	"paragraph",
	"heading",
	"bulletListItem",
	"numberedListItem",
	"checkListItem",
	"quote",
	"codeBlock",
	"table",
	"image",
	"file",
	"divider",
}

// ValidBlockType reports whether t is a supported block type.
func ValidBlockType(t string) bool {
	for _, bt := range BlockTypes {
		if bt == t {
			return true
		}
	}
	return false
}
