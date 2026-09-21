package timeentry

import (
	"context"
	"encoding/json"
	"strings"
	"time"
)

// Task 356 — the spent-time fallback: tasks that predate the
// auto-timer (Task 87) never got time_entries rows, so their
// time_spent_s stays 0 even though the status audit log records every
// in_progress stay. These helpers derive CLOSED in_progress intervals
// from an ordered `task.status_changed` timeline. Pure functions —
// readers decide the EXISTS-gate and where to stamp the derived
// seconds; nothing here persists anything.

// StatusInProgress is the status value that opens tracked work in the
// status-change timeline (matches task.StatusInProgress string-wise;
// duplicated as a literal to keep this package dependency-free).
const StatusInProgress = "in_progress"

// StatusSpentEvent is one parsed `task.status_changed` audit row used
// by the spent-time fallback (T356): the transition instant taken from
// the append-only activity log.
type StatusSpentEvent struct {
	At time.Time
	To string
	// From is empty when the payload did not carry it.
	From string
}

// StatusSpentSource is the narrow seam the fallback derivation reads:
// the status_changed audit rows for the given tasks, oldest first.
// A nil taskIDs slice means "every task that has usable rows" — the
// report fallback needs the whole legacy population because legacy
// tasks never appear in an entries-derived candidate set. Rows whose
// payload fails to parse are skipped by the implementation — the
// derivation is best-effort by contract.
//
// *sqlite.ActivityRepo satisfies it; keeping it an interface mirrors
// the TaskInfoLookup convention in the timeentry service (no storage
// import from domain code, test fixtures stay cheap).
type StatusSpentSource interface {
	StatusChangesByTasks(ctx context.Context, taskIDs []string) (map[string][]StatusSpentEvent, error)
}

// SpentInterval is one closed in_progress stay derived from the
// status timeline: [Start, End).
type SpentInterval struct {
	Start time.Time
	End   time.Time
}

// ParseStatusSpentEvents extracts usable status transitions from raw
// audit payload strings. Malformed JSON or a missing `to` value is
// dropped (the activity log is free-form JSON; one broken row must
// not poison the whole timeline). Relative order is preserved.
func ParseStatusSpentEvents(payloads []string) []StatusSpentEvent {
	out := make([]StatusSpentEvent, 0, len(payloads))
	for _, p := range payloads {
		var raw struct {
			From string `json:"from"`
			To   string `json:"to"`
		}
		if err := json.Unmarshal([]byte(p), &raw); err != nil {
			continue
		}
		if strings.TrimSpace(raw.To) == "" {
			continue
		}
		out = append(out, StatusSpentEvent{From: raw.From, To: raw.To})
	}
	return out
}

// DeriveClosedIntervals walks an ordered (oldest first) timeline and
// returns the closed in_progress stays:
//
//   - `to == "in_progress"` opens an interval, unless one is already
//     open (defensive against duplicate open rows);
//   - `from == "in_progress"` closes the currently open interval, and
//     entering ANY other status closes it too (done, review, blocked,
//     todo — every non-in_progress target is an exit);
//   - cycles (in_progress → review → in_progress → …) accumulate;
//   - an interval still open at the end of the timeline is NOT
//     returned — live work belongs to the runtime auto-timer, the
//     fallback only accounts for history.
//
// Events without a timestamp (zero At) are skipped; the repo layer
// always supplies created_at, so a zero stamp means a broken row.
func DeriveClosedIntervals(events []StatusSpentEvent) []SpentInterval {
	out := make([]SpentInterval, 0, len(events)/2+1)
	var openStart time.Time
	open := false
	closeAt := func(end time.Time) {
		if end.After(openStart) {
			out = append(out, SpentInterval{Start: openStart, End: end})
		}
	}
	for _, e := range events {
		if e.At.IsZero() {
			continue
		}
		switch {
		case e.To == StatusInProgress && !open:
			open = true
			openStart = e.At
		case open && (e.From == StatusInProgress || e.To != StatusInProgress):
			closeAt(e.At)
			open = false
		}
	}
	return out
}

// SumClipped sums the interval durations, clipping each to the
// half-open window [from, to). Zero from/to mean "no bound on that
// side" — SumClipped(ivs, time.Time{}, time.Time{}) is the raw total.
// Overlapping intervals would double-count, but the state machine in
// DeriveClosedIntervals cannot produce overlaps (one open interval at
// a time), so no merge pass is needed.
func SumClipped(ivs []SpentInterval, from, to time.Time) int64 {
	var total int64
	for _, iv := range ivs {
		start := iv.Start
		if !from.IsZero() && start.Before(from) {
			start = from
		}
		end := iv.End
		if !to.IsZero() && end.After(to) {
			end = to
		}
		if end.After(start) {
			total += int64(end.Sub(start).Seconds())
		}
	}
	return total
}

// DeriveSpentSeconds is the whole timeline total: every closed
// in_progress stay, unclipped. The card/GET fallback stamps this
// value; the report path uses DeriveClosedIntervals + SumClipped to
// respect the report window instead.
func DeriveSpentSeconds(events []StatusSpentEvent) int64 {
	var total int64
	for _, iv := range DeriveClosedIntervals(events) {
		total += int64(iv.End.Sub(iv.Start).Seconds())
	}
	return total
}
