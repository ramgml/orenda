package timeentry_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ramgml/orenda/internal/domain/timeentry"
	timeentrysvc "github.com/ramgml/orenda/internal/service/timeentry"
	"github.com/ramgml/orenda/internal/storage/sqlite"
)

// T356 spent fallback in Report: a task whose in_progress stays live
// only in the status audit log (predates the auto-timer, no
// time_entries rows) contributes derived seconds to the report.
//
//	closed intervals inside the window count, clipped to it;
//	live in_progress stays out (the runtime auto-timer owns it);
//	tasks WITH entries keep entry-derived numbers only.

// seedLegacyTimeline inserts task.status_changed rows with explicit
// created_at values — repo Create stamps datetime('now'), and the
// derivation needs controlled timestamps.
func seedLegacyTimeline(t *testing.T, db *sql.DB, taskID string, from, to time.Time) {
	t.Helper()
	enter, _ := json.Marshal(map[string]string{"from": "todo", "to": "in_progress"})
	leave, _ := json.Marshal(map[string]string{"from": "in_progress", "to": "done"})
	const layout = "2006-01-02 15:04:05"
	for _, row := range []struct {
		payload string
		at      time.Time
	}{
		{string(enter), from},
		{string(leave), to},
	} {
		_, err := db.Exec(
			`INSERT INTO task_activity (id, task_id, actor_type, actor_id, action, payload, created_at)
			 VALUES (?, ?, 'user', 'seed', 'task.status_changed', ?, ?)`,
			uuid.NewString(), taskID, row.payload, row.at.UTC().Format(layout))
		require.NoError(t, err)
	}
}

// wireSpentFallback attaches the sqlite-backed fallback the same way
// main.go does.
func wireSpentFallback(t *testing.T, svc *timeentrysvc.Service, db *sql.DB) {
	t.Helper()
	svc.WithSpentFallback(
		sqlite.NewActivityRepository(db).(*sqlite.ActivityRepo),
		sqlite.NewTimeEntryRepository(db),
	)
}

// reportTotalFor sums the per-task TotalSec rows for one task id.
func reportTotalFor(rep *timeentrysvc.AggregateReport, taskID string) int64 {
	var total int64
	for _, row := range rep.Tasks {
		if row.TaskID == taskID {
			total += row.TotalSec
		}
	}
	return total
}

func TestTimeEntryService_Report_SpentFallback_LegacyTask(t *testing.T) {
	ctx := context.Background()
	svc, taskID, agentID, _, db := setupTimeSvcFull(t)

	// Sub-second precision is dropped by the created_at column
	// layout, so anchor the timeline on truncated timestamps.
	now := time.Now().Truncate(time.Second)
	// Timeline: a closed in_progress stay of 1h, fully in the window.
	seedLegacyTimeline(t, db, taskID, now.Add(-2*time.Hour), now.Add(-time.Hour))
	wireSpentFallback(t, svc, db)

	from := now.Add(-3 * time.Hour)
	to := now.Add(time.Minute)
	rep, err := svc.Report(ctx, agentID, from, to)
	require.NoError(t, err)
	assert.Equal(t, int64(3600), reportTotalFor(rep, taskID),
		"derived in_progress stay lands in the report")

	// Window clipping: shift the start so only the second half of the
	// stay (timeline in_progress entered at -2h, left at -1h) overlaps.
	rep, err = svc.Report(ctx, agentID, from.Add(90*time.Minute), to)
	require.NoError(t, err)
	assert.Equal(t, int64(1800), reportTotalFor(rep, taskID),
		"derivation clips to the report window")
}

func TestTimeEntryService_Report_SpentFallback_EntriesWin(t *testing.T) {
	ctx := context.Background()
	svc, taskID, agentID, _, db := setupTimeSvcFull(t)

	// A legacy-style timeline PLUS a real entry: the entry-derived
	// number stands alone, no double counting.
	seedLegacyTimeline(t, db, taskID, time.Now().Add(-2*time.Hour), time.Now().Add(-time.Hour))
	end := time.Now().Truncate(time.Second)
	dur := int64(600)
	_, err := sqlite.NewTimeEntryRepository(db).CreateAndAccrue(ctx, &timeentry.TimeEntry{
		TaskID: taskID, AgentID: agentID, StartedAt: end.Add(-10 * time.Minute),
		EndedAt: &end, DurationS: &dur, Source: timeentry.SourceManual,
	})
	require.NoError(t, err)
	wireSpentFallback(t, svc, db)
	rep, err := svc.Report(ctx, agentID, time.Now().Add(-3*time.Hour), time.Now())
	require.NoError(t, err)
	assert.Equal(t, int64(600), reportTotalFor(rep, taskID),
		"entries present → derived rows never added")
}

func TestTimeEntryService_Report_SpentFallback_DisabledWithoutWiring(t *testing.T) {
	ctx := context.Background()
	svc, taskID, agentID, _, db := setupTimeSvcFull(t)

	// Timeline present but the seam NOT wired (nil SpentStatuses):
	// the report keeps pre-T356 behaviour — legacy time stays invisible.
	seedLegacyTimeline(t, db, taskID, time.Now().Add(-2*time.Hour), time.Now().Add(-time.Hour))

	rep, err := svc.Report(ctx, agentID, time.Now().Add(-3*time.Hour), time.Now())
	require.NoError(t, err)
	assert.Equal(t, int64(0), reportTotalFor(rep, taskID),
		"no wiring → no fallback rows")
}
