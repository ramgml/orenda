// Package notifier — Phase 30.5 weekly digest.
//
// The digest is a single weekly Message that summarises the owner's
// tasks over the past 7 days. It's rendered from a DigestStats
// snapshot taken at scheduler time and pushed through the same
// notifier pipeline as any other event — every bot the operator has
// subscribed gets it.
//
// Scope kept narrow by design (PLAN §30.5):
//   - stats: counts of tasks done, tasks created, awaiting-human,
//     overdue, comments left on owner's tasks, and active timers
//   - body: a short Markdown-ish summary (transports that strip
//     markdown still get useful information from the title + first
//     line of the body)
//   - no action buttons — the operator opens the UI to act on
//     anything they want to change; a digest with 5+ buttons is just
//     noise
//
// Templates are pure functions so the scheduler can hand them a
// snapshot without reaching into notifier internals.
package notifier

import (
	"fmt"
	"strings"
	"time"

	"github.com/ramgml/orenda/internal/bot"
)

// DigestStats is the data the formatter needs. The scheduler is
// responsible for assembling it; the formatter never touches the
// database. Keeps the unit test trivial: pass a stats struct, assert
// the rendered output.
type DigestStats struct {
	// Period starts at midnight 7 days before the snapshot. The
	// scheduler fills this in. Used in the body header so the
	// reader knows which week they're looking at.
	PeriodStart time.Time
	PeriodEnd   time.Time

	// OwnerID identifies the recipient. Rendered into the Message
	// target so callback-style actions (none in the current
	// template) could later reach back to the same user.
	OwnerID string

	// TasksDone counts tasks the owner transitioned to status=done
	// during the period. Includes tasks owned by the user, not just
	// tasks assigned to them.
	TasksDone int

	// TasksCreated counts new tasks landed in the inbox or any
	// project during the period.
	TasksCreated int

	// TasksAwaitingReview is the live count of tasks with
	// awaiting='human' OR status='review' at snapshot time. Not
	// "during the period" — review queue is a current-state thing,
	// and an empty queue is the most useful signal.
	TasksAwaitingReview int

	// TasksOverdue counts tasks with due_at < snapshot time and
	// status != done at snapshot time. Same rationale: a stale
	// count is the actionable signal.
	TasksOverdue int

	// CommentsReceived counts comments other users / agents left on
	// tasks owned by this user during the period.
	CommentsReceived int

	// ActiveTimers counts time_entries with ended_at IS NULL at
	// snapshot time — a sanity check that nothing leaked open.
	ActiveTimers int
}

// RenderWeeklyDigest produces the bot.Message the notifier fans out
// to the operator's subscriptions. The body is plain markdown —
// email renders it, Telegram ignores most of it, console prints it.
//
// Empty stats → "all clear" message. The owner should always get
// the digest even if nothing happened (so they can be sure the bot
// is alive); an absent digest looks like a bug.
func RenderWeeklyDigest(s DigestStats) bot.Message {
	period := formatPeriod(s.PeriodStart, s.PeriodEnd)
	title := "Weekly digest: " + period

	var lines []string
	lines = append(lines,
		fmt.Sprintf("Here's what happened on your instance between %s and %s.",
			s.PeriodStart.Format("Jan 2"), s.PeriodEnd.Format("Jan 2")),
		"")

	if s.TasksDone == 0 && s.TasksCreated == 0 &&
		s.TasksAwaitingReview == 0 && s.TasksOverdue == 0 &&
		s.CommentsReceived == 0 && s.ActiveTimers == 0 {
		lines = append(lines,
			"Nothing happened — no tasks created, nothing completed, nothing waiting for you. "+
				"This is either a quiet week or the server hasn't logged activity yet.")
	} else {
		lines = append(lines,
			fmt.Sprintf("• Tasks completed: **%d**", s.TasksDone),
			fmt.Sprintf("• Tasks created: **%d**", s.TasksCreated))
		if s.TasksAwaitingReview > 0 {
			lines = append(lines, fmt.Sprintf(
				"• Awaiting your review: **%d** — open /review to act on them",
				s.TasksAwaitingReview))
		}
		if s.TasksOverdue > 0 {
			lines = append(lines, fmt.Sprintf(
				"• Overdue: **%d** — see /today for the list",
				s.TasksOverdue))
		}
		lines = append(lines, fmt.Sprintf("• Comments received: **%d**", s.CommentsReceived))
		if s.ActiveTimers > 0 {
			lines = append(lines, fmt.Sprintf(
				"• Active timers: **%d** — stop them with /timer before they leak hours",
				s.ActiveTimers))
		}
	}

	return bot.Message{
		Kind:       "digest.weekly",
		Title:      title,
		Body:       strings.Join(lines, "\n"),
		Target:     s.OwnerID,
		CallbackID: "",
		Link:       "/today",
	}
}

// formatPeriod returns "Jan 2 – Jan 9 (7d)" style header. We don't
// use a range formatter from time package because we want a stable
// shape regardless of locale; English-only is the contract for the
// digest body (transports strip markdown but the period title is
// the first thing the operator sees).
func formatPeriod(start, end time.Time) string {
	if start.IsZero() || end.IsZero() {
		return "(unknown period)"
	}
	return fmt.Sprintf("%s – %s",
		start.Format("Jan 2"),
		end.Format("Jan 2"))
}
