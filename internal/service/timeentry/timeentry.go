// Package timeentry provides business logic for time tracking.
//
// Phase 4.5 ships Start/Stop/List with the single-active-timer
// invariant enforced via FindOpen. The activity log writes a row for
// every Start/Stop (Recorder is optional).
package timeentry

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/ramgml/orenda/internal/api/ws"
	"github.com/ramgml/orenda/internal/domain/timeentry"
)

// Sentinel errors.
var (
	ErrNotFound    = errors.New("timeentry service: not found")
	ErrAlreadyOpen = errors.New("timeentry service: agent already has an open timer")
	ErrInvalid     = errors.New("timeentry service: invalid input")

	// ErrNoActiveTimer is returned by ActiveTimer when no open
	// time-entry exists for the requested agent (or when the
	// caller passes an empty agent id). Use ErrNoActiveTimer
	// instead of `return nil, nil` so the caller can distinguish
	// "no timer" from "lookup errored".
	ErrNoActiveTimer = errors.New("timeentry service: no active timer")
)

// Recorder writes audit rows (Phase 4.5).
type Recorder interface {
	Record(ctx context.Context, action, payload string) error
}

// TaskTitleLookup is the narrow surface the report needs to enrich
// per-task rows with the task title (Phase 27.9). We don't import the
// task package directly to keep this service free of the task service
// dependency graph — main.go wires an adapter that satisfies it.
//
// Missing ids are simply absent from the result; the report renders
// a slice of the id in that case so the row stays identifiable.
type TaskTitleLookup interface {
	TitlesByIDs(ctx context.Context, ids []string) (map[string]string, error)
}

// Service is the dependency holder.
type Service struct {
	Repo     timeentry.Repository
	Hub      ws.Hub
	Recorder Recorder
	Titles   TaskTitleLookup // optional; nil disables title lookup
}

// New returns a TimeEntry service.
func New(repo timeentry.Repository, hub ws.Hub, rec Recorder) *Service {
	return &Service{Repo: repo, Hub: hub, Recorder: rec}
}

// WithTitles wires the title lookup used by Report. Returns the
// receiver so calls chain like the other With* methods on the
// service constructors.
func (s *Service) WithTitles(t TaskTitleLookup) *Service {
	s.Titles = t
	return s
}

// Start opens a new timer for (taskID, agentID). Returns ErrAlreadyOpen
// if the agent already has an open timer.
func (s *Service) Start(ctx context.Context, taskID, agentID string) (*timeentry.TimeEntry, error) {
	if taskID == "" || agentID == "" {
		return nil, ErrInvalid
	}
	e := &timeentry.TimeEntry{
		TaskID:    taskID,
		AgentID:   agentID,
		StartedAt: time.Now().UTC(),
		Source:    timeentry.SourceTimer,
	}
	got, err := s.Repo.Create(ctx, e)
	if err != nil {
		if errors.Is(err, timeentry.ErrAlreadyOpen) {
			return nil, ErrAlreadyOpen
		}
		return nil, err
	}
	s.publish(ctx, "timer.started", got)
	return got, nil
}

// findOpen is the internal helper: looks up the agent's open timer
// and returns (nil, nil) when there isn't one. Phase 20 surfaces this
// to /api/v1/today so the dashboard can show "you're working on X
// (12 min)".
func (s *Service) findOpen(ctx context.Context, agentID string) (*timeentry.TimeEntry, error) {
	return s.Repo.FindOpen(ctx, agentID)
}

// ActiveTimer returns the agent's open timer entry, or nil when no
// timer is running. Read-only — the dashboard uses this to display
// elapsed time without affecting the timer.
func (s *Service) ActiveTimer(ctx context.Context, agentID string) (*timeentry.TimeEntry, error) {
	if agentID == "" {
		return nil, ErrNoActiveTimer
	}
	return s.findOpen(ctx, agentID)
}

// OpenFor reports the agent's open timer entry when it belongs to the
// given task. Task 87's submit gate treats an open entry on the task
// as time already logged (the auto-timer holds one while the task is
// in in_progress). Returns nil when no timer is open or the open
// entry points at a different task.
func (s *Service) OpenFor(ctx context.Context, agentID, taskID string) (*timeentry.TimeEntry, error) {
	if agentID == "" {
		return nil, ErrInvalid
	}
	open, err := s.findOpen(ctx, agentID)
	if err != nil {
		return nil, err
	}
	if open == nil || open.TaskID != taskID {
		// (nil, nil) is the documented "no open timer on this task"
		// result — the submit gate treats it as "not yet logged".
		return nil, nil //nolint:nilnil // nil entry is a valid "no timer" answer
	}
	return open, nil
}

// Stop closes the agent's open timer. Returns ErrNotFound if there is
// no open timer.
func (s *Service) Stop(ctx context.Context, agentID string) (*timeentry.TimeEntry, error) {
	if agentID == "" {
		return nil, ErrInvalid
	}
	open, err := s.findOpen(ctx, agentID)
	if err != nil {
		return nil, err
	}
	if open == nil {
		return nil, ErrNotFound
	}
	now := time.Now().UTC()
	open.EndedAt = &now
	dur := int64(now.Sub(open.StartedAt).Seconds())
	open.DurationS = &dur
	// Close the entry and accrue its duration onto tasks.time_spent_s
	// in one transaction (Repository.CloseAndAccrue) — the stored
	// per-task total must never drift from the entries behind it.
	if err := s.Repo.CloseAndAccrue(ctx, open); err != nil {
		return nil, err
	}
	s.publish(ctx, "timer.stopped", open)
	return open, nil
}

// ManualAdd creates a closed entry with a user-supplied interval. Used
// when the agent (or owner) records work that wasn't tracked by the
// timer (e.g. import from another system, or retrospective work).
func (s *Service) ManualAdd(ctx context.Context, taskID, agentID string, start, end time.Time) (*timeentry.TimeEntry, error) {
	if taskID == "" || agentID == "" {
		return nil, ErrInvalid
	}
	if end.Before(start) {
		return nil, ErrInvalid
	}
	dur := int64(end.Sub(start).Seconds())
	e := &timeentry.TimeEntry{
		TaskID:    taskID,
		AgentID:   agentID,
		StartedAt: start.UTC(),
		EndedAt:   endUTC(end),
		DurationS: &dur,
		Source:    timeentry.SourceManual,
	}
	// Insert the closed manual entry and accrue its duration onto
	// tasks.time_spent_s in one transaction (Repository.CreateAndAccrue).
	got, err := s.Repo.CreateAndAccrue(ctx, e)
	if err != nil {
		return nil, err
	}
	s.publish(ctx, "timer.manual", got)
	return got, nil
}

// ListByTask returns all entries for a task.
func (s *Service) ListByTask(ctx context.Context, taskID string) ([]*timeentry.TimeEntry, error) {
	return s.Repo.ListByTask(ctx, taskID)
}

// ListByAgent returns entries for an agent in a window.
func (s *Service) ListByAgent(ctx context.Context, agentID string, from, to time.Time) ([]*timeentry.TimeEntry, error) {
	if from.After(to) {
		return nil, ErrInvalid
	}
	return s.Repo.ListByAgent(ctx, agentID, from, to)
}

// ListByDay is a convenience for the per-day report.
func (s *Service) ListByDay(ctx context.Context, agentID string, day time.Time) ([]*timeentry.TimeEntry, error) {
	return s.Repo.ListByDay(ctx, agentID, day)
}

// AggregateReport groups entries by task for a per-day report.
type AggregateReport struct {
	AgentID  string                `json:"agent_id"`
	From     time.Time             `json:"from"`
	To       time.Time             `json:"to"`
	Tasks    []AggregateReportTask `json:"tasks"`
	TotalSec int64                 `json:"total_sec"`
}

// AggregateReportTask is one row in the report.
type AggregateReportTask struct {
	TaskID   string `json:"task_id"`
	Title    string `json:"title,omitempty"`
	TotalSec int64  `json:"total_sec"`
}

// Report builds a per-task aggregation for [from, to).
//
// An empty agentID means "all actors" (Repo.ListAll): time_entries
// agent_id carries both agent UUIDs and user ids, and the report must
// show every entry unless the caller filters by one actor. A non-empty
// agentID restricts the aggregation to that actor (ListByAgent).
//
// Phase 27.9: enriches each row with the task title when a Title
// lookup is wired (via WithTitles). The lookup is one batch query
// for all distinct task ids — no N+1. Missing ids (deleted tasks)
// fall back to a slice of the id so the row stays identifiable.
func (s *Service) Report(ctx context.Context, agentID string, from, to time.Time) (*AggregateReport, error) {
	var entries []*timeentry.TimeEntry
	var err error
	if agentID == "" {
		entries, err = s.Repo.ListAll(ctx, from, to)
	} else {
		entries, err = s.Repo.ListByAgent(ctx, agentID, from, to)
	}
	if err != nil {
		return nil, err
	}
	byTask := make(map[string]int64)
	for _, e := range entries {
		d := int64(0)
		if e.DurationS != nil {
			d = *e.DurationS
		} else if e.EndedAt != nil {
			d = int64(e.EndedAt.Sub(e.StartedAt).Seconds())
		}
		byTask[e.TaskID] += d
	}
	// Batch title lookup — one query for every distinct task id
	// (Phase 27.9). The lookup is optional; nil falls back to the
	// pre-27.9 behaviour (empty title).
	var titles map[string]string
	if s.Titles != nil && len(byTask) > 0 {
		ids := make([]string, 0, len(byTask))
		for id := range byTask {
			ids = append(ids, id)
		}
		got, err := s.Titles.TitlesByIDs(ctx, ids)
		if err != nil {
			return nil, fmt.Errorf("timeentry.Report: titles lookup: %w", err)
		}
		titles = got
	}
	rep := &AggregateReport{
		AgentID: agentID,
		From:    from,
		To:      to,
		Tasks:   make([]AggregateReportTask, 0, len(byTask)),
	}
	for tid, sec := range byTask {
		row := AggregateReportTask{TaskID: tid, TotalSec: sec}
		if titles != nil {
			row.Title = titles[tid]
		}
		rep.Tasks = append(rep.Tasks, row)
		rep.TotalSec += sec
	}
	return rep, nil
}

func (s *Service) publish(ctx context.Context, eventType string, e *timeentry.TimeEntry) {
	if s.Hub == nil {
		return
	}
	s.Hub.Publish(ctx, ws.Event{
		Topic: "timers",
		Body: map[string]any{
			"type":  eventType,
			"entry": e,
		},
	})
}

// endUTC is a tiny helper to wrap end as *time.Time in UTC.
func endUTC(t time.Time) *time.Time {
	t = t.UTC()
	return &t
}

// ensure unused-import silence for fmt
var _ = fmt.Sprintf
