// Package timeentry holds the TimeEntry domain entity and repository.
//
// A TimeEntry is one agent's work interval on a specific task. The
// single-active-timer invariant (one open ended_at IS NULL per agent)
// is enforced by the service layer; the repo just stores rows.
package timeentry

import (
	"errors"
	"time"

	"github.com/ramgml/orenda/internal/domain/task"
)

// Source records how the entry was created.
type Source string

const (
	SourceTimer  Source = "timer"
	SourceManual Source = "manual"
)

// Sentinel errors.
var (
	ErrNotFound     = errors.New("timeentry: not found")
	ErrAlreadyOpen  = errors.New("timeentry: agent already has an open timer")
	ErrInvalidInput = errors.New("timeentry: invalid input")
)

// TimeEntry is one (agent, task) work interval.
type TimeEntry struct {
	ID        string     `json:"id"`
	TaskID    string     `json:"task_id"`
	AgentID   string     `json:"agent_id"`
	StartedAt time.Time  `json:"started_at"`
	EndedAt   *time.Time `json:"ended_at,omitempty"`   // nil = still running
	DurationS *int64     `json:"duration_s,omitempty"` // computed on close
	Source    Source     `json:"source"`
}

// Validate enforces invariants for new entries. EndedAt and DurationS
// are optional; the service computes DurationS when the entry closes.
func (e *TimeEntry) Validate() error {
	if e.TaskID == "" || e.AgentID == "" {
		return ErrInvalidInput
	}
	if e.StartedAt.IsZero() {
		return ErrInvalidInput
	}
	if e.EndedAt != nil && e.EndedAt.Before(e.StartedAt) {
		return ErrInvalidInput
	}
	if e.Source == "" {
		e.Source = SourceTimer
	}
	return nil
}

// ensure the project compiles even when task isn't used directly here
// (placeholder for future relations like TimeEntry -> Task).
var _ = task.StatusTodo
