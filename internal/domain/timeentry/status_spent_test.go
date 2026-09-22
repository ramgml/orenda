package timeentry_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/ramgml/orenda/internal/domain/timeentry"
)

// T356 — the spent-time fallback derivation: closed in_progress
// intervals reconstructed from the task.status_changed audit
// timeline. Table-driven per the task DoD.

func ev(at time.Time, from, to string) timeentry.StatusSpentEvent {
	return timeentry.StatusSpentEvent{At: at, From: from, To: to}
}

func TestParseStatusSpentEvents(t *testing.T) {
	t.Parallel()
	payloads := []string{
		`{"from":"todo","to":"in_progress"}`,
		`not json at all`,
		`{"from":"in_progress"}`, // no `to` — unusable
		`{"to":"review"}`,        // `from` optional, `to` required
		`{"from":"review","to":"in_progress"}`,
		`{"to":"   "}`, // whitespace-only `to` — unusable
	}
	got := timeentry.ParseStatusSpentEvents(payloads)
	assert.Len(t, got, 3)
	assert.Equal(t, "todo", got[0].From)
	assert.Equal(t, "in_progress", got[0].To)
	assert.Equal(t, "", got[1].From)
	assert.Equal(t, "review", got[1].To)
	assert.Equal(t, "review", got[2].From)
	assert.Equal(t, "in_progress", got[2].To)
}

func TestDeriveSpentSeconds(t *testing.T) {
	t.Parallel()
	base := time.Date(2026, 9, 21, 10, 0, 0, 0, time.UTC)
	m := func(minutes int) time.Time { return base.Add(time.Duration(minutes) * time.Minute) }
	ip := timeentry.StatusInProgress

	cases := []struct {
		name   string
		events []timeentry.StatusSpentEvent
		want   int64
	}{
		{
			name:   "empty timeline",
			events: nil,
			want:   0,
		},
		{
			name:   "single closed interval todo-in_progress-review",
			events: []timeentry.StatusSpentEvent{ev(m(0), "todo", ip), ev(m(30), ip, "review")},
			want:   1800,
		},
		{
			name: "multi-cycle accumulates",
			events: []timeentry.StatusSpentEvent{
				ev(m(0), "todo", ip),
				ev(m(30), ip, "review"), // cycle 1: 30m
				ev(m(60), "review", ip), // reject → back to work
				ev(m(75), ip, "review"), // cycle 2: 15m
				ev(m(90), "review", "done"),
			},
			want: 2700, // 45m
		},
		{
			name: "open interval not counted",
			events: []timeentry.StatusSpentEvent{
				ev(m(0), "todo", ip),
				// still in_progress at timeline end — live time
				// belongs to the runtime auto-timer
			},
			want: 0,
		},
		{
			name: "exit via any non-in_progress target closes",
			events: []timeentry.StatusSpentEvent{
				ev(m(0), "todo", ip),
				ev(m(20), "", "done"), // `from` missing — still an exit
			},
			want: 1200,
		},
		{
			name: "zero-duration interval skipped",
			events: []timeentry.StatusSpentEvent{
				ev(m(0), "todo", ip),
				ev(m(0), ip, "review"), // same instant → no time
				ev(m(5), "review", ip),
				ev(m(8), ip, "done"),
			},
			want: 180,
		},
		{
			name: "events without timestamps dropped",
			events: []timeentry.StatusSpentEvent{
				ev(time.Time{}, "todo", ip),
				ev(m(10), ip, "done"), // closes nothing — open never started
			},
			want: 0,
		},
		{
			name: "duplicate open rows do not reset the clock",
			events: []timeentry.StatusSpentEvent{
				ev(m(0), "todo", ip),
				ev(m(10), "review", ip), // already open — ignored
				ev(m(40), ip, "done"),   // interval measured from m(0)
			},
			want: 2400,
		},
		{
			name: "blocked detour between cycles",
			events: []timeentry.StatusSpentEvent{
				ev(m(0), "todo", ip),
				ev(m(15), ip, "blocked"), // cycle 1: 15m
				ev(m(120), "blocked", ip),
				ev(m(150), ip, "review"), // cycle 2: 30m
				ev(m(200), "review", "done"),
			},
			want: 2700, // 45m total, blocked gap excluded
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, timeentry.DeriveSpentSeconds(tc.events))
		})
	}
}

func TestSumClipped(t *testing.T) {
	t.Parallel()
	base := time.Date(2026, 9, 21, 0, 0, 0, 0, time.UTC)
	h := func(hours float64) time.Time {
		return base.Add(time.Duration(hours * float64(time.Hour)))
	}
	ivs := []timeentry.SpentInterval{
		{Start: h(1), End: h(2)},   // fully inside the window
		{Start: h(0.5), End: h(4)}, // straddles both bounds
		{Start: h(5), End: h(6)},   // fully outside (after)
	}

	t.Run("no window — raw total", func(t *testing.T) {
		t.Parallel()
		assert.Equal(t, int64(5.5*3600), timeentry.SumClipped(ivs, time.Time{}, time.Time{}))
	})
	t.Run("window inside intervals clips both sides", func(t *testing.T) {
		t.Parallel()
		// [2h, 5h): first iv contributes 2h→... clipped at its end is
		// 0 (2h..2h), straddler contributes 2h→4h = 2h; [5h,6h)
		// excluded. Wait: first iv [1,2) ends exactly at window start.
		assert.Equal(t, int64(2*3600), timeentry.SumClipped(ivs, h(2), h(5)))
	})
	t.Run("half-open window excludes interval ending at from", func(t *testing.T) {
		t.Parallel()
		// First interval [1h,2h) ends exactly at the window start —
		// [2h, 2h+ε) must contribute nothing for it.
		assert.Equal(t, int64(0), timeentry.SumClipped(ivs[:1], h(2), h(2.5)))
	})
	t.Run("window before everything", func(t *testing.T) {
		t.Parallel()
		assert.Equal(t, int64(0), timeentry.SumClipped(ivs, h(-3), h(-1)))
	})
}
