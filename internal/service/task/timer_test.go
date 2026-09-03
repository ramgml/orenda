package task_test

// Task 87: status-driven auto time tracking. Each test drives a real
// SQLite store through the task service and asserts on the time_entries
// rows plus tasks.time_spent_s — the transitions must produce exactly
// one open entry per actor and accrue closed time atomically.

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ramgml/orenda/internal/api/ws"
	activity "github.com/ramgml/orenda/internal/domain/activity"
	"github.com/ramgml/orenda/internal/domain/agent"
	"github.com/ramgml/orenda/internal/domain/project"
	"github.com/ramgml/orenda/internal/domain/task"
	"github.com/ramgml/orenda/internal/domain/timeentry"
	"github.com/ramgml/orenda/internal/domain/user"
	taskservice "github.com/ramgml/orenda/internal/service/task"
	"github.com/ramgml/orenda/internal/storage/sqlite"
	"github.com/ramgml/orenda/internal/testutil"
)

func activityActorUser() activity.ActorType  { return activity.ActorUser }
func activityActorAgent() activity.ActorType { return activity.ActorAgent }

// backdateEntry rewinds an entry's started_at so a closed interval
// carries a non-zero duration (the service stamps end=now on close).
func backdateEntry(db *sql.DB, id string, delta time.Duration) error {
	start := time.Now().UTC().Add(delta).Format("2006-01-02 15:04:05")
	_, err := db.Exec(`UPDATE time_entries SET started_at = ? WHERE id = ?`, start, id)
	return err
}

type timerHub struct{}

func (timerHub) Publish(context.Context, ws.Event) {}
func (timerHub) Close()                            {}
func (timerHub) Subscribe(string, string) (<-chan ws.Event, ws.Unsubscribe) {
	ch := make(chan ws.Event, 1)
	return ch, func() { close(ch) }
}

func setupTimerDB(t *testing.T) (*sql.DB, *taskservice.Service, *project.Project, *project.Column) {
	t.Helper()
	db, _ := testutil.TemplateDBOpen(t)

	users := sqlite.NewUserRepository(db)
	owner := &user.User{
		Email:        "timer-owner-" + newUUIDLite()[:8] + "@x.com",
		PasswordHash: "x",
		DisplayName:  "O",
	}
	require.NoError(t, users.Create(context.Background(), owner))

	projRepo := sqlite.NewProjectRepository(db)
	p, _, cols, err := projRepo.CreateProject(context.Background(), &project.Project{
		Name: "Timer", OwnerID: owner.ID,
	})
	require.NoError(t, err)

	timeRepo := sqlite.NewTimeEntryRepository(db)
	svc := taskservice.New(
		sqlite.NewTaskRepository(db),
		sqlite.NewTaskLockRepository(db),
		nil, // recorder not needed here
		nil, // comments not needed here
		timerHub{},
	)
	svc.Logger = nil
	svc.Columns = projRepo
	svc.Time = timeRepo
	return db, svc, p, cols[0]
}

func seedTimerAgent(t *testing.T, db *sql.DB, label string) string {
	t.Helper()
	tokens := sqlite.NewAPITokenRepository(db)
	users := sqlite.NewUserRepository(db)
	u := &user.User{Email: "timer-" + label + "-" + newUUIDLite()[:8] + "@x.com", PasswordHash: "x", DisplayName: "O"}
	require.NoError(t, users.Create(context.Background(), u))
	tok, err := tokens.Create(context.Background(), u.ID, "tok-"+label, "fake", "[]", nil)
	require.NoError(t, err)
	agents := sqlite.NewAgentRepository(db)
	a := &agent.Agent{Name: "timer-" + label + "-" + newUUIDLite()[:8], Type: []string{"qwen"}, TokenID: tok.ID}
	require.NoError(t, agents.Create(context.Background(), a))
	return a.ID
}

func listEntries(t *testing.T, db *sql.DB, taskID string) []*timeentry.TimeEntry {
	t.Helper()
	repo := sqlite.NewTimeEntryRepository(db)
	rows, err := repo.ListByTask(context.Background(), taskID)
	require.NoError(t, err)
	return rows
}

func timeSpent(t *testing.T, db *sql.DB, taskID string) int {
	t.Helper()
	repo := sqlite.NewTaskRepository(db)
	tr, err := repo.GetByID(context.Background(), taskID)
	require.NoError(t, err)
	return tr.TimeSpentS
}

func seedTimerTask(t *testing.T, db *sql.DB, p *project.Project, col *project.Column, title string) *task.Task {
	t.Helper()
	tasks := sqlite.NewTaskRepository(db)
	tr := &task.Task{ProjectID: p.ID, ColumnID: col.ID, Title: title}
	require.NoError(t, tasks.Create(context.Background(), tr))
	return tr
}

// DoD 1: claim opens an entry for the assignee; the stale-guard closes
// a leftover open interval from a previous claim instead of double-
// opening.
func TestAutoTimer_Claim_OpensEntry(t *testing.T) {
	db, svc, p, col := setupTimerDB(t)
	tr := seedTimerTask(t, db, p, col, "claim-timer")
	agentID := seedTimerAgent(t, db, "c1")

	claimed, err := svc.Claim(context.Background(), tr.ID, agentID)
	require.NoError(t, err)

	entries := listEntries(t, db, tr.ID)
	require.Len(t, entries, 1)
	assert.Equal(t, agentID, entries[0].AgentID)
	assert.Equal(t, tr.ID, entries[0].TaskID)
	assert.Nil(t, entries[0].EndedAt, "entry must be open while in_progress")
	assert.Equal(t, timeentry.SourceTimer, entries[0].Source)
	_ = claimed
}

// DoD 1 + 4: submit (→ review) closes the entry with a duration and
// accrues it onto tasks.time_spent_s.
func TestAutoTimer_Submit_ClosesEntryAndAccrues(t *testing.T) {
	db, svc, p, col := setupTimerDB(t)
	tr := seedTimerTask(t, db, p, col, "submit-timer")
	agentID := seedTimerAgent(t, db, "c2")

	_, err := svc.Claim(context.Background(), tr.ID, agentID)
	require.NoError(t, err)

	// Age the open entry so the accrued duration is non-zero.
	entries := listEntries(t, db, tr.ID)
	require.Len(t, entries, 1)
	require.NoError(t, backdateEntry(db, entries[0].ID, -90*time.Second))

	_, err = svc.Submit(context.Background(), tr.ID, agentID, "")
	require.NoError(t, err)

	entries = listEntries(t, db, tr.ID)
	require.Len(t, entries, 1)
	require.NotNil(t, entries[0].EndedAt)
	require.NotNil(t, entries[0].DurationS)
	assert.Positive(t, *entries[0].DurationS)
	assert.Equal(t, int(*entries[0].DurationS), timeSpent(t, db, tr.ID),
		"tasks.time_spent_s must equal the closed entry duration")
}

// DoD 1 + 5: reject re-opens the timer (a new open entry), approve
// closes what reject opened — rework time is tracked end to end.
func TestAutoTimer_RejectReopens_ApproveCloses(t *testing.T) {
	db, svc, p, col := setupTimerDB(t)
	tr := seedTimerTask(t, db, p, col, "review-timer")
	agentID := seedTimerAgent(t, db, "c3")

	_, err := svc.Claim(context.Background(), tr.ID, agentID)
	require.NoError(t, err)
	entries := listEntries(t, db, tr.ID)
	require.Len(t, entries, 1)
	require.NoError(t, backdateEntry(db, entries[0].ID, -60*time.Second))

	_, err = svc.Submit(context.Background(), tr.ID, agentID, "")
	require.NoError(t, err)

	_, err = svc.Review(context.Background(), tr.ID, "owner-x", taskservice.ReviewReject, "needs work")
	require.NoError(t, err)

	entries = listEntries(t, db, tr.ID)
	require.Len(t, entries, 2, "closed interval + reopened interval")
	open := 0
	for _, e := range entries {
		if e.EndedAt == nil {
			open++
			assert.Equal(t, agentID, e.AgentID)
		}
	}
	assert.Equal(t, 1, open, "exactly one open entry after reject-reopen")
	assert.Positive(t, timeSpent(t, db, tr.ID), "first interval already accrued")

	_, err = svc.Review(context.Background(), tr.ID, "owner-x", taskservice.ReviewApprove, "ok")
	require.NoError(t, err)

	entries = listEntries(t, db, tr.ID)
	require.Len(t, entries, 2)
	for _, e := range entries {
		assert.NotNil(t, e.EndedAt, "approve must leave no open entry")
	}
	assert.Equal(t, int(*entries[0].DurationS)+int(*entries[1].DurationS), timeSpent(t, db, tr.ID))
}

// DoD 4: release closes the open interval even though release clears
// the assignee — the actor is captured before the mutation.
func TestAutoTimer_Release_ClosesEntry(t *testing.T) {
	db, svc, p, col := setupTimerDB(t)
	tr := seedTimerTask(t, db, p, col, "release-timer")
	agentID := seedTimerAgent(t, db, "c4")

	_, err := svc.Claim(context.Background(), tr.ID, agentID)
	require.NoError(t, err)
	entries := listEntries(t, db, tr.ID)
	require.Len(t, entries, 1)
	require.NoError(t, backdateEntry(db, entries[0].ID, -30*time.Second))

	_, err = svc.Release(context.Background(), tr.ID, agentID)
	require.NoError(t, err)

	entries = listEntries(t, db, tr.ID)
	require.Len(t, entries, 1)
	require.NotNil(t, entries[0].EndedAt, "release must close the auto-timer entry")
	assert.Positive(t, timeSpent(t, db, tr.ID))
}

// DoD 4 (stale-guard): claiming with an already-open interval on
// another task closes the stale interval (accruing it) and opens a
// fresh one — the one-open-timer invariant holds and no time is lost.
func TestAutoTimer_StaleGuard_ClosesOldAndOpensNew(t *testing.T) {
	db, svc, p, col := setupTimerDB(t)
	first := seedTimerTask(t, db, p, col, "stale-first")
	second := seedTimerTask(t, db, p, col, "stale-second")
	agentID := seedTimerAgent(t, db, "c5")

	_, err := svc.Claim(context.Background(), first.ID, agentID)
	require.NoError(t, err)
	entries := listEntries(t, db, first.ID)
	require.Len(t, entries, 1)
	require.NoError(t, backdateEntry(db, entries[0].ID, -120*time.Second))

	// Simulate a lost release: claim the second task while the first
	// interval is still open.
	_, err = svc.Claim(context.Background(), second.ID, agentID)
	require.NoError(t, err)

	firstEntries := listEntries(t, db, first.ID)
	require.Len(t, firstEntries, 1)
	require.NotNil(t, firstEntries[0].EndedAt, "stale interval must be closed")
	assert.Positive(t, timeSpent(t, db, first.ID), "stale interval must accrue")

	secondEntries := listEntries(t, db, second.ID)
	require.Len(t, secondEntries, 1)
	assert.Nil(t, secondEntries[0].EndedAt, "new interval must be open")
}

// DoD 5: owner kanban-move through SyncAndSave drives the timer too —
// entering in_progress opens (actor = owner fallback for unassigned),
// leaving closes.
func TestAutoTimer_SyncAndSave_OwnerMove(t *testing.T) {
	db, svc, p, col := setupTimerDB(t)
	tr := seedTimerTask(t, db, p, col, "sync-timer")

	// Owner drags the card onto the in_progress column.
	tr.Status = task.StatusInProgress
	require.NoError(t, svc.SyncAndSave(context.Background(), tr, "owner-x", activityActorUser(), task.StatusTodo))
	entries := listEntries(t, db, tr.ID)
	require.Len(t, entries, 1)
	assert.Nil(t, entries[0].EndedAt)
	assert.NotEmpty(t, entries[0].AgentID, "actor falls back to project owner")
	ownerActor := entries[0].AgentID

	// Drag back out of in_progress.
	tr.Status = task.StatusTodo
	require.NoError(t, svc.SyncAndSave(context.Background(), tr, "owner-x", activityActorUser(), task.StatusInProgress))
	entries = listEntries(t, db, tr.ID)
	require.Len(t, entries, 1)
	require.NotNil(t, entries[0].EndedAt)
	_ = ownerActor
}

// DoD 1: no status change → no timer churn (idempotence).
func TestAutoTimer_NoStatusChange_NoChurn(t *testing.T) {
	db, svc, p, col := setupTimerDB(t)
	tr := seedTimerTask(t, db, p, col, "churn-timer")
	agentID := seedTimerAgent(t, db, "c6")

	_, err := svc.Claim(context.Background(), tr.ID, agentID)
	require.NoError(t, err)

	// A PATCH that keeps in_progress must not close/reopen anything.
	require.NoError(t, svc.SyncAndSave(context.Background(), tr, agentID, activityActorAgent(), task.StatusInProgress))
	entries := listEntries(t, db, tr.ID)
	require.Len(t, entries, 1)
	assert.Nil(t, entries[0].EndedAt, "same-status sync must keep the interval")
}

// Task 92: the Release persist must not clobber time_spent_s. The
// pre-92 path re-read the counter (after the timer sync) and wrote
// it back through the full-row Update, so a CloseAndAccrue
// committing between that re-read and the write was silently lost.
//
// The race is simulated deterministically through the Service.Tasks
// seam: a wrapper injects the concurrent accrual (the same relative
// UPDATE the repo issues) right after the third interface-level
// GetByID — which on the pre-92 code is the post-sync re-read whose
// value the full-row Update then clobbers. On the fixed code the
// third GetByID is the post-persist refresh, the injected accrual
// lands after it and must survive to the final read.
type accrualAfterNthRead struct {
	task.Repository
	db     *sql.DB
	taskID string
	n      int // inject after the n-th GetByID (1-based)
	seen   int
	err    error
}

func (r *accrualAfterNthRead) GetByID(ctx context.Context, id string) (*task.Task, error) {
	tr, err := r.Repository.GetByID(ctx, id)
	if err == nil && id == r.taskID {
		r.seen++
		if r.seen == r.n {
			// A concurrent actor's CloseAndAccrue commits now.
			_, r.err = r.db.Exec(
				`UPDATE tasks SET time_spent_s = time_spent_s + 45 WHERE id = ?`, id)
		}
	}
	return tr, err
}

func TestAutoTimer_Release_DoesNotClobberConcurrentAccrual(t *testing.T) {
	db, svc, p, col := setupTimerDB(t)
	tr := seedTimerTask(t, db, p, col, "release-race-timer")
	agentID := seedTimerAgent(t, db, "c7")

	wrapped := &accrualAfterNthRead{
		Repository: sqlite.NewTaskRepository(db),
		db:         db, taskID: tr.ID,
		n: 3, // claim fetch, release fetch, release last counter read
	}
	svc.Tasks = wrapped

	_, err := svc.Claim(context.Background(), tr.ID, agentID)
	require.NoError(t, err)
	entries := listEntries(t, db, tr.ID)
	require.Len(t, entries, 1)
	require.NoError(t, backdateEntry(db, entries[0].ID, -60*time.Second))

	_, err = svc.Release(context.Background(), tr.ID, agentID)
	require.NoError(t, err)
	require.NoError(t, wrapped.err, "concurrent accrual injection must succeed")
	require.Equal(t, 3, wrapped.seen, "Release must re-read the row exactly once")

	got := timeSpent(t, db, tr.ID)
	assert.GreaterOrEqual(t, got, 60+45,
		"release persist must not clobber a concurrently accrued time_spent_s")
}

// Repo-level pin: ClearAssigneeToTodo writes the release columns
// and nothing else — time_spent_s is out of its SET list by
// contract.
func TestTaskRepo_ClearAssigneeToTodo_LeavesCounterAlone(t *testing.T) {
	db, svc, p, col := setupTimerDB(t)
	tr := seedTimerTask(t, db, p, col, "partial-update-timer")
	agentID := seedTimerAgent(t, db, "c8")

	_, err := svc.Claim(context.Background(), tr.ID, agentID)
	require.NoError(t, err)
	require.NoError(t, backdateEntry(db, listEntries(t, db, tr.ID)[0].ID, -30*time.Second))
	_, err = svc.Submit(context.Background(), tr.ID, agentID, "")
	require.NoError(t, err)
	before := timeSpent(t, db, tr.ID)
	require.Positive(t, before)

	repo := sqlite.NewTaskRepository(db)
	stale, err := repo.GetByID(context.Background(), tr.ID)
	require.NoError(t, err)
	stale.AssigneeType = ""
	stale.AssigneeID = ""
	stale.Status = task.StatusTodo
	stale.Awaiting = task.AwaitingNone
	stale.TimeSpentS = 999999 // would be persisted by the full-row Update
	require.NoError(t, repo.ClearAssigneeToTodo(context.Background(), tr.ID, stale))

	assert.Equal(t, before, timeSpent(t, db, tr.ID),
		"ClearAssigneeToTodo must not write time_spent_s")
	got, err := repo.GetByID(context.Background(), tr.ID)
	require.NoError(t, err)
	assert.Equal(t, task.StatusTodo, got.Status)
	assert.Empty(t, got.AssigneeID)
	assert.Equal(t, task.AwaitingNone, got.Awaiting)

	// Release-contract parity: a nonexistent id surfaces ErrNotFound.
	err = repo.ClearAssigneeToTodo(context.Background(), "no-such", stale)
	assert.ErrorIs(t, err, task.ErrNotFound)
}
