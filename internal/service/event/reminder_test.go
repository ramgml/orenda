package event

import (
	"context"
	"errors"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ramgml/orenda/internal/domain/event"
	"github.com/ramgml/orenda/internal/service/notifier"
)

// fakeRepo implements event.Repository minimally for the Reminder test.
type fakeRepo struct {
	events []*event.Event
	err    error
}

func (f *fakeRepo) GetByID(_ context.Context, id string) (*event.Event, error) {
	for _, e := range f.events {
		if e.ID == id {
			return e, nil
		}
	}
	return nil, nil
}
func (f *fakeRepo) GetBySlug(context.Context, string) (*event.Event, error) {
	return nil, nil
}
func (f *fakeRepo) List(context.Context) ([]*event.Event, error) { return f.events, nil }
func (f *fakeRepo) ListInRange(_ context.Context, from, to time.Time, _ string) ([]*event.Event, error) {
	if f.err != nil {
		return nil, f.err
	}
	out := make([]*event.Event, 0)
	for _, e := range f.events {
		if (e.StartAt.After(from) || e.StartAt.Equal(from)) && e.StartAt.Before(to) {
			out = append(out, e)
		}
	}
	return out, nil
}
func (f *fakeRepo) Create(context.Context, *event.Event) (*event.Event, error) {
	return nil, nil
}
func (f *fakeRepo) Update(context.Context, *event.Event) error { return nil }
func (f *fakeRepo) Delete(context.Context, string) error       { return nil }

// Reminder.scan is called by Run once per tick; we drive it directly
// via a custom Tick channel to keep the test fast and deterministic.
func TestReminder_FiresForUpcomingEvents(t *testing.T) {
	now := time.Date(2026, 8, 9, 10, 0, 0, 0, time.UTC)
	repo := &fakeRepo{events: []*event.Event{
		{ID: "ev-1", Title: "Stand-up", StartAt: now.Add(45 * time.Minute)},
		{ID: "ev-2", Title: "Lunch", StartAt: now.Add(2 * time.Hour)}, // outside window
		{ID: "ev-3", Title: "1:1", StartAt: now.Add(59 * time.Minute)},
	}}
	var fired int32
	r := &Reminder{
		Repo: repo,
		Notify: func(_ context.Context, e notifier.Event) error {
			atomic.AddInt32(&fired, 1)
			assert.Equal(t, "event.upcoming_1h", e.Type)
			return nil
		},
		NotifyProjectOwner: func(_ context.Context, id string) (string, string, string, error) {
			return "owner-1", "title", "/cal", nil
		},
		Lead:   30 * time.Minute,
		Window: 30 * time.Minute,
		Now:    func() time.Time { return now },
		Log:    slog.Default(),
	}
	require.NoError(t, r.RunScanForTest(context.Background()))
	assert.EqualValues(t, 2, atomic.LoadInt32(&fired), "only events in the 30–60min band should fire")
}

func TestReminder_OwnerLookupFailureSkipsEvent(t *testing.T) {
	now := time.Date(2026, 8, 9, 10, 0, 0, 0, time.UTC)
	repo := &fakeRepo{events: []*event.Event{
		{ID: "ev-1", Title: "x", StartAt: now.Add(40 * time.Minute)},
	}}
	var fired int32
	r := &Reminder{
		Repo: repo,
		Notify: func(context.Context, notifier.Event) error {
			atomic.AddInt32(&fired, 1)
			return nil
		},
		NotifyProjectOwner: func(context.Context, string) (string, string, string, error) {
			return "", "", "", errors.New("owner lookup failed")
		},
		Lead:   30 * time.Minute,
		Window: 30 * time.Minute,
		Now:    func() time.Time { return now },
		Log:    slog.Default(),
	}
	require.NoError(t, r.RunScanForTest(context.Background()))
	assert.EqualValues(t, 0, atomic.LoadInt32(&fired), "owner-lookup failures should not fire")
}

func TestReminder_NoEventsInWindow(t *testing.T) {
	now := time.Date(2026, 8, 9, 10, 0, 0, 0, time.UTC)
	repo := &fakeRepo{events: []*event.Event{
		{ID: "ev-1", Title: "x", StartAt: now.Add(10 * time.Minute)}, // too soon
		{ID: "ev-2", Title: "y", StartAt: now.Add(3 * time.Hour)},    // too far
	}}
	var fired int32
	r := &Reminder{
		Repo: repo,
		Notify: func(context.Context, notifier.Event) error {
			atomic.AddInt32(&fired, 1)
			return nil
		},
		NotifyProjectOwner: func(context.Context, string) (string, string, string, error) {
			return "o", "t", "/", nil
		},
		Lead:   30 * time.Minute,
		Window: 30 * time.Minute,
		Now:    func() time.Time { return now },
		Log:    slog.Default(),
	}
	require.NoError(t, r.RunScanForTest(context.Background()))
	assert.EqualValues(t, 0, atomic.LoadInt32(&fired))
}

func TestReminder_NilDepsNoPanic(t *testing.T) {
	// When Notify/NotifyProjectOwner are nil the scheduler must skip
	// cleanly rather than crash. Used to fail with a nil-pointer panic
	// before the nil-guard landed.
	r := &Reminder{
		Repo: &fakeRepo{events: []*event.Event{{ID: "x", StartAt: time.Now().Add(45 * time.Minute)}}},
		Log:  slog.Default(),
	}
	assert.NotPanics(t, func() { _ = r.RunScanForTest(context.Background()) })
}
