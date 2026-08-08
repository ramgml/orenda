package event_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ramgml/orenda/internal/api/ws"
	"github.com/ramgml/orenda/internal/domain/event"
	eventsvc "github.com/ramgml/orenda/internal/service/event"
	"github.com/ramgml/orenda/internal/storage/sqlite"
)

type memHub struct{ n int }

func (h *memHub) Publish(_ context.Context, _ ws.Event) { h.n++ }

func (h *memHub) Subscribe(string, string) (<-chan ws.Event, ws.Unsubscribe) {
	ch := make(chan ws.Event, 1)
	return ch, func() { close(ch) }
}

func setupEventSvc(t *testing.T) (*eventsvc.Service, *memHub, *sql.DB) {
	t.Helper()
	dir := t.TempDir()
	db, err := sqlite.Open(context.Background(), filepath.Join(dir+"/ev.db"), sqlite.OpenConfig{
		WALMode: true, EnableForeign: true, BusyTimeoutMs: 5000,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	require.NoError(t, sqlite.Migrate(context.Background(), db, sqlite.MigrationsFS, "migrations"))

	hub := &memHub{}
	svc := eventsvc.New(sqlite.NewEventRepository(db), hub, nil)
	return svc, hub, db
}

func TestEventService_CreateAndListInRange(t *testing.T) {
	svc, hub, _ := setupEventSvc(t)
	start := time.Now().Add(-time.Hour).Truncate(time.Second)
	end := start.Add(2 * time.Hour)
	e := &event.Event{Title: "Stand-up", StartAt: start, EndAt: end}
	got, err := svc.Create(context.Background(), e)
	require.NoError(t, err)
	assert.NotEmpty(t, got.ID)
	assert.Equal(t, 1, hub.n, "expected 1 WS event")

	// ListInRange finds it.
	from := start.Add(-30 * time.Minute)
	to := start.Add(30 * time.Minute)
	list, err := svc.ListInRange(context.Background(), from, to, "")
	require.NoError(t, err)
	assert.Len(t, list, 1)
}

func TestEventService_UpdateAndDelete(t *testing.T) {
	svc, _, _ := setupEventSvc(t)
	got, err := svc.Create(context.Background(), &event.Event{
		Title: "x", StartAt: time.Now(), EndAt: time.Now().Add(time.Hour),
	})
	require.NoError(t, err)

	got.Title = "y"
	require.NoError(t, svc.Update(context.Background(), got))
	got2, err := svc.Create(context.Background(), &event.Event{
		Title: "z", StartAt: time.Now(), EndAt: time.Now().Add(time.Hour),
	})
	require.NoError(t, err)
	_ = got2

	require.NoError(t, svc.Delete(context.Background(), got.ID))
}

func TestEventService_NotFoundMapping(t *testing.T) {
	svc, _, _ := setupEventSvc(t)
	err := svc.Update(context.Background(), &event.Event{
		ID: "no-such", Title: "x", StartAt: time.Now(), EndAt: time.Now().Add(time.Hour),
	})
	assert.ErrorIs(t, err, eventsvc.ErrNotFound)

	err = svc.Delete(context.Background(), "no-such")
	assert.ErrorIs(t, err, eventsvc.ErrNotFound)
}

func TestEventService_ExpandRecurrence_Daily(t *testing.T) {
	svc, _, _ := setupEventSvc(t)
	start := time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)
	e := &event.Event{
		Title:      "Daily standup",
		StartAt:    start,
		EndAt:      start.Add(15 * time.Minute),
		Recurrence: "FREQ=DAILY;COUNT=5",
	}
	from := start
	to := start.Add(7 * 24 * time.Hour)
	occs, err := svc.ExpandRecurrence(e, from, to)
	require.NoError(t, err)
	assert.Len(t, occs, 5, "expected 5 daily occurrences")
	// First occurrence at start, second at start+1d, etc.
	for i, occ := range occs {
		want := start.AddDate(0, 0, i)
		assert.True(t, occ.StartAt.Equal(want), "occurrence %d at %s, want %s", i, occ.StartAt, want)
	}
}

func TestEventService_ExpandRecurrence_WeeklyWithInterval(t *testing.T) {
	svc, _, _ := setupEventSvc(t)
	start := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC) // Friday
	e := &event.Event{
		Title:      "Bi-weekly retro",
		StartAt:    start,
		EndAt:      start.Add(time.Hour),
		Recurrence: "FREQ=WEEKLY;INTERVAL=2;COUNT=3",
	}
	occs, err := svc.ExpandRecurrence(e, start, start.AddDate(0, 2, 0))
	require.NoError(t, err)
	assert.Len(t, occs, 3, "expected 3 bi-weekly occurrences")
	assert.Equal(t, start, occs[0].StartAt)
	assert.Equal(t, 14*24*time.Hour, occs[1].StartAt.Sub(occs[0].StartAt))
}

func TestEventService_ExpandRecurrence_NoRecurrence(t *testing.T) {
	svc, _, _ := setupEventSvc(t)
	start := time.Now().Truncate(time.Second)
	e := &event.Event{Title: "One-off", StartAt: start, EndAt: start.Add(time.Hour)}
	occs, err := svc.ExpandRecurrence(e, start.Add(-time.Hour), start.Add(2*time.Hour))
	require.NoError(t, err)
	assert.Len(t, occs, 1)
}

func TestEventService_ExpandRecurrence_InvalidRange(t *testing.T) {
	svc, _, _ := setupEventSvc(t)
	_, err := svc.ExpandRecurrence(&event.Event{}, time.Now(), time.Now().Add(-time.Hour))
	assert.ErrorIs(t, err, eventsvc.ErrInvalidInput)
}

func TestEventService_ExpandRecurrence_BadRule(t *testing.T) {
	svc, _, _ := setupEventSvc(t)
	e := &event.Event{Title: "x", Recurrence: "FREQ=NOPE;COUNT=5"}
	_, err := svc.ExpandRecurrence(e, time.Now(), time.Now().Add(time.Hour))
	assert.ErrorIs(t, err, eventsvc.ErrBadRecurrence)
}

var _ = strings.ReplaceAll
