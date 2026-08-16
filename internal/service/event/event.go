// Package event — calendar "events" are tasks with a time range
// (StartAt/EndAt). The legacy events table was folded into tasks in
// Phase 11 (migration 012); this package keeps the public API and
// DTO shape so the REST surface (/api/v1/events) and frontend callers
// stay stable.
//
// The Service here is a thin facade over task.Service: it converts
// task <-> event DTOs and runs the same publish hooks. New code
// should work with tasks directly via task.Service.
package event

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/ramgml/orenda/internal/api/ws"
	"github.com/ramgml/orenda/internal/domain/event"
	"github.com/ramgml/orenda/internal/domain/task"
)

// Sentinel errors.
var (
	ErrNotFound      = errors.New("event service: not found")
	ErrInvalidInput  = errors.New("event service: invalid input")
	ErrBadRecurrence = errors.New("event service: malformed recurrence rule")
)

// Recorder writes audit rows on every mutation.
type Recorder interface {
	Record(ctx context.Context, actorID, action, payload string) error
}

// Service is the dependency holder.
type Service struct {
	Tasks    task.Repository
	Hub      ws.Hub
	Recorder Recorder
}

// New returns an Event service.
func New(repo task.Repository, hub ws.Hub, rec Recorder) *Service {
	return &Service{Tasks: repo, Hub: hub, Recorder: rec}
}

// Create persists a new event by inserting a task with a time range.
// Title, start_at and end_at are required; project_id is OPTIONAL —
// events can land in the Inbox (project_id IS NULL) just like any
// other task. When a project IS supplied, the event is parked in
// that project's first kanban column; otherwise it's an inbox card.
func (s *Service) Create(ctx context.Context, e *event.Event) (*event.Event, error) {
	if err := e.Validate(); err != nil {
		if errors.Is(err, event.ErrInvalidInput) {
			return nil, ErrInvalidInput
		}
		return nil, err
	}
	t := taskFromEvent(e, e.ProjectID)
	// An empty t.ID is fine — the task repo assigns a UUIDv7 on Create.
	// Park the new task in the project's first kanban column so it
	// shows up on the project page. Without this, column_id stays
	// NULL and the kanban's "group by column" loop never renders
	// the task anywhere. Inbox tasks (no project) deliberately stay
	// column_id = NULL — there's no board for them.
	if t.ColumnID == "" && t.ProjectID != "" {
		if cid, err := s.Tasks.FirstColumnID(ctx, t.ProjectID); err != nil {
			return nil, err
		} else if cid != "" {
			t.ColumnID = cid
		}
	}
	got, err := s.createTask(ctx, t)
	if err != nil {
		return nil, err
	}
	s.publish(ctx, "event.created", got)
	return eventFromTask(got), nil
}

// Get returns a single event by id (= task id).
func (s *Service) Get(ctx context.Context, id string) (*event.Event, error) {
	if id == "" {
		return nil, ErrInvalidInput
	}
	tr, err := s.Tasks.GetByID(ctx, id)
	if err != nil {
		if errors.Is(err, task.ErrNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	if tr.StartAt == nil || tr.EndAt == nil {
		return nil, ErrNotFound
	}
	return eventFromTask(tr), nil
}

// Update mutates an existing event. The task id is used as the
// stable handle; everything else gets persisted to the underlying
// task row.
func (s *Service) Update(ctx context.Context, e *event.Event) error {
	if err := e.Validate(); err != nil {
		return err
	}
	if e.ID == "" {
		return ErrInvalidInput
	}
	tr, err := s.Tasks.GetByID(ctx, e.ID)
	if err != nil {
		if errors.Is(err, task.ErrNotFound) {
			return ErrNotFound
		}
		return err
	}
	merged := mergeEventIntoTask(e, tr)
	if err := s.Tasks.Update(ctx, merged); err != nil {
		return err
	}
	s.publish(ctx, "event.updated", merged)
	return nil
}

// publish sends a WS event. Body keeps the underlying task so the
// frontend can refetch its row.

// Delete removes an event (= task) by id.
func (s *Service) Delete(ctx context.Context, id string) error {
	if err := s.Tasks.Delete(ctx, id); err != nil {
		if errors.Is(err, task.ErrNotFound) {
			return ErrNotFound
		}
		return err
	}
	s.publish(ctx, "event.deleted", &task.Task{ID: id})
	return nil
}

// ListInRange returns timed tasks (start_at NOT NULL) whose interval
// overlaps [from, to]. projectID empty = all projects.
func (s *Service) ListInRange(ctx context.Context, from, to time.Time, projectID string) ([]*event.Event, error) {
	if from.After(to) {
		return nil, ErrInvalidInput
	}
	tasks, err := s.Tasks.ListInRange(ctx, from, to, projectID)
	if err != nil {
		return nil, err
	}
	out := make([]*event.Event, 0, len(tasks))
	for _, tr := range tasks {
		out = append(out, eventFromTask(tr))
	}
	return out, nil
}

// ExpandRecurrence returns the occurrences of e that fall in [from, to).
//
// Supported subset of RFC 5545 RRULE:
//
//	FREQ=DAILY|WEEKLY|MONTHLY
//	INTERVAL=N        (default 1)
//	COUNT=N           (max number of occurrences)
//	UNTIL=YYYYMMDDTHHMMSSZ  (cap on occurrences)
//
// Master events with no recurrence are returned as a single occurrence
// when they overlap the window. Unparseable rules are tolerated
// (treated as no recurrence) so a typo in a saved rule doesn't break
// the calendar.
func (s *Service) ExpandRecurrence(e *event.Event, from, to time.Time) ([]event.Occurrence, error) {
	if from.After(to) {
		return nil, ErrInvalidInput
	}
	interval, count, until, err := parseRRule(e.Recurrence)
	if err != nil {
		return nil, err
	}
	if interval <= 0 {
		interval = 1
	}

	var out []event.Occurrence
	step := stepFor(e.Recurrence, interval)
	if step == nil {
		// Unparseable: return the master once if it overlaps.
		if e.StartAt.Before(to) && e.EndAt.After(from) {
			out = append(out, event.Occurrence{Event: *e, StartAt: e.StartAt, EndAt: e.EndAt, MasterID: e.ID})
		}
		return out, nil
	}

	cur := e.StartAt
	for i := 0; i < 10000; i++ { // safety cap
		if count > 0 && i >= count {
			break
		}
		if !until.IsZero() && cur.After(until) {
			break
		}
		occEnd := cur.Add(e.EndAt.Sub(e.StartAt))
		if cur.Before(to) && occEnd.After(from) {
			out = append(out, event.Occurrence{
				Event:    *e,
				StartAt:  cur,
				EndAt:    occEnd,
				MasterID: e.ID,
			})
		}
		if cur.After(to) && (count == 0 && until.IsZero()) {
			break // no upper bound; we've already passed the window
		}
		cur = step(cur)
	}
	return out, nil
}

// parseRRule returns (interval, count, until, err). Empty rule → all
// zero values, no error.
func parseRRule(rule string) (interval int, count int, until time.Time, err error) {
	rule = strings.TrimSpace(rule)
	if rule == "" {
		return 0, 0, time.Time{}, nil
	}
	var freq string
	for _, part := range strings.Split(rule, ";") {
		kv := strings.SplitN(strings.TrimSpace(part), "=", 2)
		if len(kv) != 2 {
			return 0, 0, time.Time{}, fmt.Errorf("%w: %q", ErrBadRecurrence, part)
		}
		switch strings.ToUpper(kv[0]) {
		case "FREQ":
			freq = strings.ToUpper(kv[1])
		case "INTERVAL":
			interval, err = strconv.Atoi(kv[1])
		case "COUNT":
			count, err = strconv.Atoi(kv[1])
		case "UNTIL":
			until, err = time.Parse("20060102T150405Z", kv[1])
		}
		if err != nil {
			return 0, 0, time.Time{}, fmt.Errorf("%w: %s=%s", ErrBadRecurrence, kv[0], kv[1])
		}
	}
	switch freq {
	case "DAILY", "WEEKLY", "MONTHLY":
	default:
		return 0, 0, time.Time{}, fmt.Errorf("%w: FREQ=%q", ErrBadRecurrence, freq)
	}
	return interval, count, until, nil
}

// stepFor returns a function that advances cur by the RRULE interval
// for the given FREQ, or nil if FREQ is unrecognised.
func stepFor(rule string, interval int) func(time.Time) time.Time {
	var freq string
	for _, part := range strings.Split(rule, ";") {
		kv := strings.SplitN(strings.TrimSpace(part), "=", 2)
		if len(kv) == 2 && strings.ToUpper(kv[0]) == "FREQ" {
			freq = strings.ToUpper(kv[1])
		}
	}
	switch freq {
	case "DAILY":
		return func(t time.Time) time.Time { return t.AddDate(0, 0, interval) }
	case "WEEKLY":
		return func(t time.Time) time.Time { return t.AddDate(0, 0, 7*interval) }
	case "MONTHLY":
		return func(t time.Time) time.Time { return t.AddDate(0, interval, 0) }
	}
	return nil
}

// taskFromEvent copies calendar fields onto a task skeleton. The
// task.Status defaults to "todo"; the actual column is resolved
// at insert time by the repository (it picks the first column of the
// project).
//
// Phase 23.3: Recurrence is carried across so a saved RRULE
// survives the events→tasks fold and the calendar can expand
// occurrences server-side on list.
func taskFromEvent(e *event.Event, projectID string) *task.Task {
	start, end := e.StartAt, e.EndAt
	return &task.Task{
		ID:          e.ID,
		ProjectID:   projectID,
		Title:       e.Title,
		Description: e.Description,
		Status:      task.StatusTodo,
		Priority:    task.PriorityMedium,
		Awaiting:    task.AwaitingNone,
		StartAt:     &start,
		EndAt:       &end,
		AllDay:      e.AllDay,
		Color:       e.Color,
		Recurrence:  e.Recurrence,
	}
}

// mergeEventIntoTask copies the PATCH-supplied fields onto tr.

// mergeEventIntoTask copies the PATCH-supplied fields onto tr.
//
// Phase 16: ProjectID is assigned UNCONDITIONALLY — a PATCH with
// `project_id: ""` is the user's way of moving an event back to the
// Inbox. The previous `if e.ProjectID != ""` guard silently ignored
// clear intents, which would leave events stranded on a project the
// user wanted to unfile. Callers (handlers_calendar.go) must not
// invoke this with e.ProjectID unset — they always send the field,
// using "" for "Inbox".
func mergeEventIntoTask(e *event.Event, tr *task.Task) *task.Task {
	tr.Title = e.Title
	if e.Description != "" {
		tr.Description = e.Description
	}
	if !e.StartAt.IsZero() {
		s := e.StartAt
		tr.StartAt = &s
	}
	if !e.EndAt.IsZero() {
		en := e.EndAt
		tr.EndAt = &en
	}
	tr.AllDay = e.AllDay
	if e.Color != "" {
		tr.Color = e.Color
	}
	// Phase 16: unconditional. "" means "Inbox" — clear the column too
	// if we just de-projected the event, otherwise the FK stays
	// pointing at a column on a board that's gone (or will be).
	prev := tr.ProjectID
	tr.ProjectID = e.ProjectID
	if tr.ProjectID == "" && prev != "" {
		tr.ColumnID = ""
	}
	// Phase 23.3: carry the RRULE across so the calendar keeps
	// expanding the series after the user edits anything else.
	tr.Recurrence = e.Recurrence
	return tr
}

func eventFromTask(tr *task.Task) *event.Event {
	return &event.Event{
		ID:         tr.ID,
		Title:      tr.Title,
		StartAt:    *tr.StartAt,
		EndAt:      *tr.EndAt,
		AllDay:     tr.AllDay,
		Color:      tr.Color,
		ProjectID:  tr.ProjectID,
		Recurrence: tr.Recurrence,
	}
}

func (s *Service) publish(ctx context.Context, eventType string, tr *task.Task) {
	if s.Hub == nil {
		return
	}
	s.Hub.Publish(ctx, ws.Event{
		Topic: "events",
		Body: map[string]any{
			"type": eventType,
			"task": tr,
		},
	})
}

// createTask persists a task with calendar fields and resolves its
// column by picking the second column of the project's default board
// ("todo") — or the first column if the board has only one.
func (s *Service) createTask(ctx context.Context, t *task.Task) (*task.Task, error) {
	// We don't have a direct Repo helper to "first column of project";
	// instead we keep t.ColumnID empty and let the storage layer (or
	// post-create patch) figure it out. For now we patch via Tasks.Update.
	if t.ColumnID == "" {
		// We can't look up a column without a project.Repo dependency;
		// the FK on column_id is SET NULL so leaving it empty is safe —
		// the task will show up in the kanban at column_id=null which
		// the UI treats as "unscheduled".
	}
	if err := s.Tasks.Create(ctx, t); err != nil {
		return nil, fmt.Errorf("event.Create: %w", err)
	}
	got, err := s.Tasks.GetByID(ctx, t.ID)
	if err != nil {
		return nil, err
	}
	return got, nil
}

// ---------------------------------------------------------------------------
// Recurrence helpers. ExpandRecurrence above is live: the calendar list
// handler (handlers_calendar.go) expands RRULE masters into occurrences
// for the requested window (Phase 23.3).
// ---------------------------------------------------------------------------
