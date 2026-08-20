// Package course — Phase 32.12 velocity + drift types.
//
// VelocityStats summarises a course's recent lesson-completion rate.
// It's read-only from the API surface; nothing writes to it.
//
// Drift classifies the gap between the user's actual pace and the
// target that study-proposals (Phase 31) imply. The classifier is
// deliberately small — three states — because the planner uses the
// signal to scale its proposals, not to alarm anyone.
package course

import "time"

// VelocityStats summarises a course's lesson-completion rate over a
// rolling window. The window is passed by the caller (14d for the
// agent-side /courses?status=active endpoint). All-zero values mean
// "no data" — neither the planner nor the UI should panic on this;
// legacy data and brand-new courses legitimately show zero until the
// student finishes their first lesson.
type VelocityStats struct {
	// LessonsDoneInWindow is the count of done-lessons whose
	// completed_at falls inside [since, now]. NULL completed_at
	// (pre-migration 025) is excluded.
	LessonsDoneInWindow int
	// LastCompletedAt is the max(completed_at) across all done-
	// lessons, regardless of window — null when no done-lesson has
	// a non-null timestamp. Drives the UI's "last activity" pill.
	LastCompletedAt *time.Time
	// Since / Window echo the inputs so the API consumer can reason
	// about staleness without a separate /config call.
	Since  time.Time
	Window time.Duration
}

// ActualVelocityPerWeek is the metric the planner consumes:
// LessonsDoneInWindow divided by the window expressed in weeks.
// Zero when the window is empty or the user hasn't completed
// anything.
func (v VelocityStats) ActualVelocityPerWeek() float64 {
	if v.Window <= 0 {
		return 0
	}
	return float64(v.LessonsDoneInWindow) * float64(time.Hour*24*7) / float64(v.Window)
}

// Drift is the classification the planner uses to scale proposals
// (Phase 32.12, wiki lms-pace-adaptation §Дизайн-вопросы).
//
// Classification rules:
//
//	no data  → on_track (don't panic without evidence)
//	actual >= target                    → ahead (proposing less is fine)
//	actual <  target * 0.7              → behind (proposing more is fine)
//	otherwise                            → on_track
//
// "Target" is study-proposals accepted in the same window. The
// point of measuring against accepted proposals (not pace_notes_md)
// is that pace_notes is free-form text — we don't parse it; the
// user/agent has to translate it into proposals before the planner
// sees it.
type Drift string

const (
	DriftAhead   Drift = "ahead"
	DriftOnTrack Drift = "on_track"
	DriftBehind  Drift = "behind"
)

// ClassifyDrift returns the drift state for a (actual, target)
// velocity pair. The "no data" case (both zero) returns on_track —
// the planner keeps proposing as usual, just doesn't try to "catch
// up" or "ease off" until it has a signal. See the Drift doc above.
//
// `actual` is the user's measured velocity in lessons/week. `target`
// is the same unit computed from accepted study-proposals. The
// 0.7/1.0/1.3 thresholds match the wiki's "30% deviation over a
// 2-week rolling window" intent — anything within ±30% is on_track.
func ClassifyDrift(actual, target float64) Drift {
	if actual == 0 && target == 0 {
		return DriftOnTrack
	}
	// No target → no signal; the planner's current proposal cadence
	// is the only thing the user has, so don't flag drift.
	if target == 0 {
		return DriftOnTrack
	}
	// No actual but target > 0 → the student is behind their own
	// scheduled pace (proposals went out, lessons didn't get done).
	if actual == 0 {
		return DriftBehind
	}
	ratio := actual / target
	switch {
	case ratio >= 1.3:
		return DriftAhead
	case ratio < 0.7:
		return DriftBehind
	default:
		return DriftOnTrack
	}
}
