// Package event provides business logic for calendar events.
//
// Phase 4.4 ships Create/Update/Delete plus ExpandRecurrence: a tiny
// RFC 5545 RRULE subset that handles DAILY/WEEKLY/MONTHLY with optional
// COUNT/UNTIL. Full RRULE parsing lands in Phase 5 if needed.
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
)

// Sentinel errors.
var (
	ErrNotFound      = errors.New("event service: not found")
	ErrInvalidInput  = errors.New("event service: invalid input")
	ErrBadRecurrence = errors.New("event service: malformed recurrence rule")
)

// Recorder writes audit rows on every mutation (Phase 4.4 onward).
// Phase 4 doesn't yet wire a real activity repo; the service accepts
// nil and no-ops.
type Recorder interface {
	Record(ctx context.Context, actorID, action, payload string) error
}

// Service is the dependency holder.
type Service struct {
	Repo     event.Repository
	Hub      ws.Hub
	Recorder Recorder
}

// New returns an Event service.
func New(repo event.Repository, hub ws.Hub, rec Recorder) *Service {
	return &Service{Repo: repo, Hub: hub, Recorder: rec}
}

// Create persists a new event. WS event: event.created.
func (s *Service) Create(ctx context.Context, e *event.Event) (*event.Event, error) {
	if err := e.Validate(); err != nil {
		return nil, err
	}
	got, err := s.Repo.Create(ctx, e)
	if err != nil {
		return nil, err
	}
	s.publish(ctx, "event.created", got, "")
	return got, nil
}

// Update mutates an existing event. WS event: event.updated.
func (s *Service) Update(ctx context.Context, e *event.Event) error {
	if err := e.Validate(); err != nil {
		return err
	}
	if err := s.Repo.Update(ctx, e); err != nil {
		if errors.Is(err, event.ErrNotFound) {
			return ErrNotFound
		}
		return err
	}
	got, _ := s.Repo.GetByID(ctx, e.ID)
	s.publish(ctx, "event.updated", got, "")
	return nil
}

// Delete removes an event by id.
func (s *Service) Delete(ctx context.Context, id string) error {
	if err := s.Repo.Delete(ctx, id); err != nil {
		if errors.Is(err, event.ErrNotFound) {
			return ErrNotFound
		}
		return err
	}
	s.publish(ctx, "event.deleted", &event.Event{ID: id}, "")
	return nil
}

// ListInRange returns events in [from, to) — master events with their
// recurrence rules already set. The client is responsible for expanding
// recurrence via ExpandRecurrence if it needs per-occurrence views.
func (s *Service) ListInRange(ctx context.Context, from, to time.Time, projectID string) ([]*event.Event, error) {
	if from.After(to) {
		return nil, ErrInvalidInput
	}
	return s.Repo.ListInRange(ctx, from, to, projectID)
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
// when they overlap the window. Unparseable rules are tolerated (treated
// as no recurrence) so a typo in a saved rule doesn't break the calendar.
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

// stepFor returns a function that advances time by one interval, or nil
// if FREQ is missing/unsupported.
func stepFor(rule string, interval int) func(time.Time) time.Time {
	freq := ""
	for _, part := range strings.Split(rule, ";") {
		kv := strings.SplitN(strings.TrimSpace(part), "=", 2)
		if len(kv) == 2 && strings.EqualFold(kv[0], "FREQ") {
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
	default:
		return nil
	}
}

// publish emits a WS event.
func (s *Service) publish(ctx context.Context, eventType string, e *event.Event, actorID string) {
	if s.Hub == nil {
		return
	}
	s.Hub.Publish(ctx, ws.Event{
		Topic: "events",
		Body: map[string]any{
			"type":  eventType,
			"event": e,
			"actor": actorID,
		},
	})
}
