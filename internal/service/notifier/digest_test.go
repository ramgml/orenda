package notifier

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func mustParse(t *testing.T, s string) time.Time {
	t.Helper()
	tm, err := time.Parse("2006-01-02", s)
	require.NoError(t, err)
	return tm
}

// TestRenderWeeklyDigest_AllClear covers the empty-stats branch —
// the most common case for a healthy install.
func TestRenderWeeklyDigest_AllClear(t *testing.T) {
	msg := RenderWeeklyDigest(DigestStats{
		OwnerID:     "user-1",
		PeriodStart: mustParse(t, "2026-08-09"),
		PeriodEnd:   mustParse(t, "2026-08-16"),
	})
	assert.Equal(t, "digest.weekly", msg.Kind)
	assert.Contains(t, msg.Title, "Weekly digest")
	assert.Contains(t, msg.Title, "Aug 9")
	assert.Contains(t, msg.Title, "Aug 16")
	assert.Contains(t, msg.Body, "Nothing happened")
	assert.Equal(t, "user-1", msg.Target)
}

// TestRenderWeeklyDigest_PopulatedRendersAllBullets: when each
// counter is non-zero, every corresponding bullet appears in the
// body. We pin the exact lines so a copy-edit doesn't silently
// remove the actionable signals.
func TestRenderWeeklyDigest_PopulatedRendersAllBullets(t *testing.T) {
	msg := RenderWeeklyDigest(DigestStats{
		OwnerID:             "user-1",
		PeriodStart:         mustParse(t, "2026-08-09"),
		PeriodEnd:           mustParse(t, "2026-08-16"),
		TasksDone:           4,
		TasksCreated:        12,
		TasksAwaitingReview: 3,
		TasksOverdue:        2,
		CommentsReceived:    5,
		ActiveTimers:        1,
	})
	assert.Contains(t, msg.Body, "Tasks completed: **4**")
	assert.Contains(t, msg.Body, "Tasks created: **12**")
	assert.Contains(t, msg.Body, "Awaiting your review: **3**")
	assert.Contains(t, msg.Body, "Overdue: **2**")
	assert.Contains(t, msg.Body, "Comments received: **5**")
	assert.Contains(t, msg.Body, "Active timers: **1**")
	assert.NotContains(t, msg.Body, "Nothing happened")
}

// TestRenderWeeklyDigest_ZeroReviewOmitsLine: when awaiting_review is
// 0, we deliberately drop the line (no noise). Owner should not
// see "Awaiting your review: 0" because it doesn't tell them
// anything they couldn't infer from an empty /review screen.
func TestRenderWeeklyDigest_ZeroReviewOmitsLine(t *testing.T) {
	msg := RenderWeeklyDigest(DigestStats{
		OwnerID:             "user-1",
		PeriodStart:         mustParse(t, "2026-08-09"),
		PeriodEnd:           mustParse(t, "2026-08-16"),
		TasksDone:           2,
		TasksCreated:        5,
		TasksAwaitingReview: 0,
		TasksOverdue:        1,
		CommentsReceived:    3,
	})
	assert.NotContains(t, msg.Body, "Awaiting your review")
	assert.Contains(t, msg.Body, "Tasks completed: **2**")
	assert.Contains(t, msg.Body, "Overdue: **1**")
}

// TestRenderWeeklyDigest_PeriodHeaderIsReadable: the title contains
// both endpoints. Operators glance at this in their inbox; if it's
// gibberish they think the bot is broken.
func TestRenderWeeklyDigest_PeriodHeaderIsReadable(t *testing.T) {
	msg := RenderWeeklyDigest(DigestStats{
		PeriodStart: mustParse(t, "2026-08-09"),
		PeriodEnd:   mustParse(t, "2026-08-16"),
	})
	assert.True(t, strings.HasPrefix(msg.Title, "Weekly digest: "),
		"title must include the prefix")
	assert.Contains(t, msg.Title, "Aug 9 – Aug 16")
}

// TestRenderWeeklyDigest_LinkTargetsToday: digest always links to
// /today, the operator's daily-driver screen. Don't change this —
// the muscle memory is "digest = /today".
func TestRenderWeeklyDigest_LinkTargetsToday(t *testing.T) {
	msg := RenderWeeklyDigest(DigestStats{
		OwnerID: "user-1",
	})
	assert.Equal(t, "/today", msg.Link)
}

// TestRenderWeeklyDigest_NoActions: digest is informational, not
// actionable. Buttons would just be noise; we deliberately ship
// zero. If a future phase adds quick actions, this test is the
// guard that needs updating.
func TestRenderWeeklyDigest_NoActions(t *testing.T) {
	msg := RenderWeeklyDigest(DigestStats{
		OwnerID: "user-1",
	})
	assert.Empty(t, msg.Actions,
		"weekly digest should not carry action buttons")
}

// TestRenderWeeklyDigest_BodyIsSaneOnZeroDates: defensive guard —
// if the scheduler forgets to fill period, the title degrades to
// "(unknown period)" rather than panicking or rendering "Jan 1".
func TestRenderWeeklyDigest_BodyIsSaneOnZeroDates(t *testing.T) {
	msg := RenderWeeklyDigest(DigestStats{OwnerID: "user-1"})
	assert.Contains(t, msg.Title, "unknown period")
	assert.NotEmpty(t, msg.Body)
}
