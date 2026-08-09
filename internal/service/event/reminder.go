// Package service — event reminder scheduler.
//
// Scans upcoming events in the [now+Lead, now+Lead+Window] window
// every Tick and emits a notification per event for the project
// owner. The dedup_key on each notification is
// "event.upcoming_1h:<event_id>" so re-runs within the window collapse
// to a single inbox row.
//
// PRD F-C-4 promised "Уведомление за N минут до события". This is the
// implementation — kept deliberately simple: one lead time, one
// window, one shot per event. Recurrence is the caller's problem
// (the calendar's ListInRange already expands master events).
package event

import (
	"context"
	"log/slog"
	"time"

	"github.com/ramgml/orenda/internal/domain/event"
	notifierservice "github.com/ramgml/orenda/internal/service/notifier"
)

// Reminder scans for events starting soon and notifies the owner.
//
//   - Lead: how soon an event must start before we notify (default 30m)
//   - Window: how wide the lookahead band is (default 30m)
//   - Tick: how often we scan (default 60s)
//   - Now: clock seam for tests (defaults to time.Now)
type Reminder struct {
	Repo   event.Repository
	Notify func(context.Context, notifierservice.Event) error

	Lead   time.Duration
	Window time.Duration
	Tick   time.Duration

	// NotifyProjectOwner resolves the project's owner user id for an
	// event. Without it we can't route the notification.
	NotifyProjectOwner func(ctx context.Context, eventID string) (ownerID, title, link string, err error)

	Now func() time.Time
	Log *slog.Logger
}

// Run blocks until ctx is cancelled, ticking every Reminder.Tick.
func (r *Reminder) Run(ctx context.Context) {
	r.applyDefaults()
	t := time.NewTicker(r.Tick)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := r.scan(ctx); err != nil && r.Log != nil {
				r.Log.Warn("event reminder scan failed", "err", err)
			}
		}
	}
}

// RunScanForTest runs one scan synchronously. Used by tests so they
// don't need to wait for a Tick to fire.
func (r *Reminder) RunScanForTest(ctx context.Context) error {
	r.applyDefaults()
	return r.scan(ctx)
}

// applyDefaults fills in zero-valued Reminder fields with sane
// defaults so Run / RunScanForTest behave consistently.
func (r *Reminder) applyDefaults() {
	if r.Tick <= 0 {
		r.Tick = time.Minute
	}
	if r.Lead <= 0 {
		r.Lead = 30 * time.Minute
	}
	if r.Window <= 0 {
		r.Window = 30 * time.Minute
	}
	if r.Now == nil {
		r.Now = time.Now
	}
	if r.Log == nil {
		r.Log = slog.Default()
	}
}

// scan is one pass: list events in [now+Lead, now+Lead+Window] and
// emit a single notification per event.
func (r *Reminder) scan(ctx context.Context) error {
	if r.Notify == nil || r.NotifyProjectOwner == nil {
		return nil
	}
	now := r.Now().UTC()
	from := now.Add(r.Lead)
	to := now.Add(r.Lead + r.Window)
	events, err := r.Repo.ListInRange(ctx, from, to, "")
	if err != nil {
		return err
	}
	for _, ev := range events {
		ownerID, title, link, err := r.NotifyProjectOwner(ctx, ev.ID)
		if err != nil {
			r.Log.Warn("reminder: owner lookup failed", "event_id", ev.ID, "err", err)
			continue
		}
		if ownerID == "" {
			continue
		}
		// Send the notification. Dedup via the standard
		// event.upcoming_1h:<event_id> key the templates use.
		if err := r.Notify(ctx, notifierservice.Event{
			Type:       "event.upcoming_1h",
			UserID:     ownerID,
			TargetType: "event",
			TargetID:   ev.ID,
			Title:      "Upcoming: " + title,
			Body:       "Starts at " + ev.StartAt.Format("15:04"),
			Link:       link,
			DedupKey:   "event.upcoming_1h:" + ev.ID,
		}); err != nil {
			r.Log.Warn("reminder: notify failed", "event_id", ev.ID, "err", err)
		}
	}
	return nil
}
