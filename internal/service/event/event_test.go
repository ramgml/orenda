package event_test

import (
	"context"
	"path/filepath"
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

func setupEventSvc(t *testing.T) (*eventsvc.Service, *memHub) {
	t.Helper()
	dir := t.TempDir()
	db, err := sqlite.Open(context.Background(), filepath.Join(dir+"/ev.db"), sqlite.OpenConfig{
		WALMode: true, EnableForeign: true, BusyTimeoutMs: 5000,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	require.NoError(t, sqlite.Migrate(context.Background(), db, sqlite.MigrationsFS, "migrations"))

	hub := &memHub{}
	svc := eventsvc.New(sqlite.NewTaskRepository(db), hub, nil)
	return svc, hub
}

func TestEventService_CreateAndListInRange(t *testing.T) {
	svc, hub := setupEventSvc(t)
	svc.DefaultProjectID = "00000000-0000-0000-0000-00000000cafe"
	start := time.Now().Add(-time.Hour).Truncate(time.Second)
	end := start.Add(2 * time.Hour)
	e := &event.Event{Title: "Stand-up", StartAt: start, EndAt: end}
	got, err := svc.Create(context.Background(), e)
	require.NoError(t, err)
	assert.NotEmpty(t, got.ID)
	assert.Equal(t, "Stand-up", got.Title)
	assert.Equal(t, hub.n, 1, "expected one WS publish")

	from := start.Add(-time.Hour)
	to := start.Add(3 * time.Hour)
	list, err := svc.ListInRange(context.Background(), from, to, "")
	require.NoError(t, err)
	assert.Len(t, list, 1)
}

func TestEventService_CreateWithoutProjectUsesDefault(t *testing.T) {
	svc, _ := setupEventSvc(t)
	svc.DefaultProjectID = "00000000-0000-0000-0000-00000000cafe"

	start := time.Now().Add(time.Hour)
	e := &event.Event{Title: "No project", StartAt: start, EndAt: start.Add(time.Hour)}
	got, err := svc.Create(context.Background(), e)
	require.NoError(t, err)
	assert.Equal(t, "00000000-0000-0000-0000-00000000cafe", got.ProjectID,
		"missing project_id should fall back to DefaultProjectID")
}

func TestEventService_CreateRejectsEmptyTitle(t *testing.T) {
	svc, _ := setupEventSvc(t)
	e := &event.Event{Title: "", StartAt: time.Now(), EndAt: time.Now().Add(time.Hour)}
	_, err := svc.Create(context.Background(), e)
	assert.ErrorIs(t, err, eventsvc.ErrInvalidInput)
}

func TestEventService_ExpandRecurrence_Daily(t *testing.T) {
	svc, _ := setupEventSvc(t)
	start := time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)
	e := &event.Event{
		Title:      "Daily standup",
		StartAt:    start,
		EndAt:      start.Add(30 * time.Minute),
		Recurrence: "FREQ=DAILY;COUNT=5",
	}
	from := start
	to := start.Add(7 * 24 * time.Hour)
	occs, err := svc.ExpandRecurrence(e, from, to)
	require.NoError(t, err)
	assert.Len(t, occs, 5, "expected 5 daily occurrences")
	for i, occ := range occs {
		want := start.AddDate(0, 0, i)
		assert.True(t, occ.StartAt.Equal(want), "occurrence %d at %s, want %s", i, occ.StartAt, want)
	}
}

func TestEventService_ExpandRecurrence_WeeklyWithInterval(t *testing.T) {
	svc, _ := setupEventSvc(t)
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
	svc, _ := setupEventSvc(t)
	start := time.Now().Truncate(time.Second)
	e := &event.Event{Title: "One-off", StartAt: start, EndAt: start.Add(time.Hour)}
	occs, err := svc.ExpandRecurrence(e, start.Add(-time.Hour), start.Add(2*time.Hour))
	require.NoError(t, err)
	assert.Len(t, occs, 1)
}

func TestEventService_ExpandRecurrence_InvalidRange(t *testing.T) {
	svc, _ := setupEventSvc(t)
	_, err := svc.ExpandRecurrence(&event.Event{}, time.Now(), time.Now().Add(-time.Hour))
	assert.ErrorIs(t, err, eventsvc.ErrInvalidInput)
}

func TestEventService_ExpandRecurrence_BadRule(t *testing.T) {
	svc, _ := setupEventSvc(t)
	e := &event.Event{Title: "x", Recurrence: "FREQ=NOPE;COUNT=5"}
	_, err := svc.ExpandRecurrence(e, time.Now(), time.Now().Add(time.Hour))
	assert.ErrorIs(t, err, eventsvc.ErrBadRecurrence)
}
