// Package course — Phase 32.12 drift classifier tests.
//
// ClassifyDrift is the heart of the pace-adaptation feature.
// Behaviour table:
//
//	(0, 0)         → on_track  (no data)
//	(0, target>0)  → behind     (student is behind their own proposed schedule)
//	(actual>0, 0) → on_track  (no schedule to compare against)
//	(actual/target < 0.7)  → behind
//	(actual/target ≥ 1.3)   → ahead
//	(0.7 ≤ ratio < 1.3)     → on_track
package course

import "testing"

func TestClassifyDrift(t *testing.T) {
	cases := []struct {
		name           string
		actual, target float64
		want           Drift
	}{
		{"no data: both zero", 0, 0, DriftOnTrack},
		{"no data: target zero, actual positive", 1.5, 0, DriftOnTrack},
		{"behind: zero actual, positive target", 0, 2.0, DriftBehind},
		{"behind: 50% of target", 1.0, 2.0, DriftBehind},
		{"behind: just under threshold", 1.39, 2.0, DriftBehind},
		{"on_track: 80% of target", 1.6, 2.0, DriftOnTrack},
		{"on_track: 100% of target", 2.0, 2.0, DriftOnTrack},
		{"on_track: 120% of target", 2.4, 2.0, DriftOnTrack},
		{"ahead: just over threshold", 2.61, 2.0, DriftAhead},
		{"ahead: 200% of target", 4.0, 2.0, DriftAhead},
		{"extreme ahead: 5x target", 10.0, 2.0, DriftAhead},
		{"extreme behind: 0.05x target", 0.1, 2.0, DriftBehind},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ClassifyDrift(tc.actual, tc.target)
			if got != tc.want {
				t.Errorf("ClassifyDrift(%v, %v) = %q, want %q",
					tc.actual, tc.target, got, tc.want)
			}
		})
	}
}

// TestVelocityStats_ActualVelocityPerWeek checks the rate math: zero
// window → zero rate (defensive), and a 1-lesson-in-14d window should
// return 0.5 lessons/week.
func TestVelocityStats_ActualVelocityPerWeek(t *testing.T) {
	cases := []struct {
		name   string
		stats  VelocityStats
		expect float64
	}{
		{
			name:   "zero window — defensive zero",
			stats:  VelocityStats{LessonsDoneInWindow: 5, Window: 0},
			expect: 0,
		},
		{
			name:   "1 lesson in 14 days → 0.5/wk",
			stats:  VelocityStats{LessonsDoneInWindow: 1, Window: 14 * 24 * 60 * 60 * 1e9},
			expect: 0.5,
		},
		{
			name:   "7 lessons in 14 days → 3.5/wk",
			stats:  VelocityStats{LessonsDoneInWindow: 7, Window: 14 * 24 * 60 * 60 * 1e9},
			expect: 3.5,
		},
		{
			name:   "0 lessons → 0/wk (no data, not panicking)",
			stats:  VelocityStats{LessonsDoneInWindow: 0, Window: 14 * 24 * 60 * 60 * 1e9},
			expect: 0,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.stats.ActualVelocityPerWeek()
			if diff := got - tc.expect; diff < -1e-9 || diff > 1e-9 {
				t.Errorf("ActualVelocityPerWeek() = %v, want %v", got, tc.expect)
			}
		})
	}
}
