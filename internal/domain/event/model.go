// Package event holds the Event domain entity and repository interface.
//
// Events are calendar entries the owner (or an agent) creates. They can
// optionally be linked to a project (so the calendar can group events
// per project) and recur via a simple RFC 5545 RRULE subset.
package event

import (
	"errors"
	"time"
)

// Sentinel errors.
var (
	ErrNotFound     = errors.New("event: not found")
	ErrInvalidInput = errors.New("event: invalid input")
)

// Event is the canonical entity.
type Event struct {
	ID          string    `json:"id"`
	Title       string    `json:"title"`
	Description string    `json:"description,omitempty"`
	StartAt     time.Time `json:"start_at"`
	EndAt       time.Time `json:"end_at"`
	AllDay      bool      `json:"all_day"`
	Color       string    `json:"color,omitempty"`
	ProjectID   string    `json:"project_id,omitempty"`
	Recurrence  string    `json:"recurrence,omitempty"` // RRULE subset, "" = none
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// Validate enforces input invariants.
func (e *Event) Validate() error {
	if e.Title == "" {
		return ErrInvalidInput
	}
	if e.StartAt.IsZero() || e.EndAt.IsZero() {
		return ErrInvalidInput
	}
	if e.EndAt.Before(e.StartAt) {
		return ErrInvalidInput
	}
	if e.AllDay {
		// Normalise to day boundaries; storage layer keeps the raw times.
		e.StartAt = time.Date(e.StartAt.Year(), e.StartAt.Month(), e.StartAt.Day(), 0, 0, 0, 0, e.StartAt.Location())
		e.EndAt = time.Date(e.EndAt.Year(), e.EndAt.Month(), e.EndAt.Day(), 23, 59, 59, 0, e.EndAt.Location())
	}
	return nil
}

// Occurrence is one expanded instance of an event, after recurrence
// expansion. The Recurrence field is empty (we only store it on the
// master event; occurrences inherit it).
type Occurrence struct {
	Event    Event     `json:"event"`
	StartAt  time.Time `json:"start_at"`
	EndAt    time.Time `json:"end_at"`
	MasterID string    `json:"master_id"`
}
