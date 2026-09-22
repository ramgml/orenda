package timeentry_test

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ramgml/orenda/internal/api/ws"
	"github.com/ramgml/orenda/internal/domain/agent"
	"github.com/ramgml/orenda/internal/domain/project"
	"github.com/ramgml/orenda/internal/domain/task"
	"github.com/ramgml/orenda/internal/domain/user"
	timeentrysvc "github.com/ramgml/orenda/internal/service/timeentry"
	"github.com/ramgml/orenda/internal/storage/sqlite"
	"github.com/ramgml/orenda/internal/testutil"
)

type memHub struct{ n int }

func (h *memHub) Publish(_ context.Context, _ ws.Event) { h.n++ }

// Close implements ws.Hub (Phase 22.3).
func (h *memHub) Close() {}

func (h *memHub) Subscribe(string, string) (<-chan ws.Event, ws.Unsubscribe) {
	ch := make(chan ws.Event, 1)
	return ch, func() { close(ch) }
}

type memRecorder struct{ n int }

func (r *memRecorder) Record(_ context.Context, _ string, _ string) error {
	r.n++
	return nil
}

// setupTimeSvc keeps the historical four-value signature most tests
// use; setupTimeSvcFull also returns the raw *sql.DB for tests that
// build a second repository (T354 project grouping test).
func setupTimeSvc(t *testing.T) (*timeentrysvc.Service, string, string, task.Repository) {
	svc, taskID, agentID, tasks, _ := setupTimeSvcFull(t)
	return svc, taskID, agentID, tasks
}

func setupTimeSvcFull(t *testing.T) (*timeentrysvc.Service, string, string, task.Repository, *sql.DB) {
	t.Helper()
	db, _ := testutil.TemplateDBOpen(t)

	users := sqlite.NewUserRepository(db)
	owner := &user.User{
		Email:        "ts-" + strings.ReplaceAll(t.Name(), "/", "-") + "-" + newIDLite()[:8] + "@x.com",
		PasswordHash: "x",
		DisplayName:  "Owner",
	}
	require.NoError(t, users.Create(context.Background(), owner))

	tokens := sqlite.NewAPITokenRepository(db)
	tok, err := tokens.Create(context.Background(), owner.ID, "ts-tok", "fake", "[]", nil)
	require.NoError(t, err)
	agents := sqlite.NewAgentRepository(db)
	a := &agent.Agent{Name: "ts-" + newIDLite()[:6], Type: []string{"qwen"}, TokenID: tok.ID}
	require.NoError(t, agents.Create(context.Background(), a))

	projects := sqlite.NewProjectRepository(db)
	p, _, cols, err := projects.CreateProject(context.Background(), &project.Project{
		Name: "TS", OwnerID: owner.ID,
	})
	require.NoError(t, err)

	tasks := sqlite.NewTaskRepository(db)
	tr := &task.Task{ProjectID: p.ID, ColumnID: cols[0].ID, Title: "t"}
	require.NoError(t, tasks.Create(context.Background(), tr))

	hub := &memHub{}
	svc := timeentrysvc.New(sqlite.NewTimeEntryRepository(db), hub, &memRecorder{})
	return svc, tr.ID, a.ID, tasks, db
}

func newIDLite() string {
	const hex = "0123456789abcdef"
	b := make([]byte, 36)
	for i := range b {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			b[i] = '-'
			continue
		}
		b[i] = hex[(i*7)%16]
	}
	return string(b)
}

func TestTimeEntryService_StartStop(t *testing.T) {
	svc, taskID, agentID, _ := setupTimeSvc(t)

	got, err := svc.Start(context.Background(), taskID, agentID)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Nil(t, got.EndedAt)
	assert.Nil(t, got.DurationS)

	time.Sleep(1100 * time.Millisecond) // >1s so duration is non-zero

	closed, err := svc.Stop(context.Background(), agentID)
	require.NoError(t, err)
	require.NotNil(t, closed)
	require.NotNil(t, closed.EndedAt)
	require.NotNil(t, closed.DurationS)
	assert.GreaterOrEqual(t, *closed.DurationS, int64(1))
}

func TestTimeEntryService_StartWhenAlreadyOpen(t *testing.T) {
	svc, taskID, agentID, _ := setupTimeSvc(t)
	_, err := svc.Start(context.Background(), taskID, agentID)
	require.NoError(t, err)
	_, err = svc.Start(context.Background(), taskID, agentID)
	assert.ErrorIs(t, err, timeentrysvc.ErrAlreadyOpen)
}

func TestTimeEntryService_StopWithoutOpen(t *testing.T) {
	svc, _, agentID, _ := setupTimeSvc(t)
	_, err := svc.Stop(context.Background(), agentID)
	assert.ErrorIs(t, err, timeentrysvc.ErrNotFound)
}

func TestTimeEntryService_ManualAdd(t *testing.T) {
	svc, taskID, agentID, _ := setupTimeSvc(t)
	start := time.Now().Add(-time.Hour).Truncate(time.Second)
	end := start.Add(30 * time.Minute)
	got, err := svc.ManualAdd(context.Background(), taskID, agentID, start, end)
	require.NoError(t, err)
	require.NotNil(t, got.EndedAt)
	require.NotNil(t, got.DurationS)
	assert.Equal(t, int64(30*60), *got.DurationS)
}

func TestTimeEntryService_ManualAdd_RejectsBadRange(t *testing.T) {
	svc, taskID, agentID, _ := setupTimeSvc(t)
	now := time.Now()
	_, err := svc.ManualAdd(context.Background(), taskID, agentID, now, now.Add(-time.Hour))
	assert.ErrorIs(t, err, timeentrysvc.ErrInvalid)
}

func TestTimeEntryService_ReportAggregatesPerTask(t *testing.T) {
	svc, taskID, agentID, _ := setupTimeSvc(t)
	now := time.Now().Truncate(time.Second)

	// Add 3 entries on the same task, 30 minutes each.
	for i := 0; i < 3; i++ {
		end := now.Add(time.Duration(i) * time.Hour).Add(30 * time.Minute)
		_, err := svc.ManualAdd(context.Background(), taskID, agentID,
			now.Add(time.Duration(i)*time.Hour), end)
		require.NoError(t, err)
	}

	from := now.Add(-time.Hour)
	to := now.Add(3 * time.Hour)
	rep, err := svc.Report(context.Background(), agentID, from, to)
	require.NoError(t, err)
	assert.Len(t, rep.Tasks, 1)
	assert.Equal(t, int64(90*60), rep.Tasks[0].TotalSec)
	assert.Equal(t, int64(90*60), rep.TotalSec)
}

func TestTimeEntryService_ListByDay(t *testing.T) {
	svc, taskID, agentID, _ := setupTimeSvc(t)
	now := time.Now().Truncate(time.Second)
	end := now.Add(30 * time.Minute)
	_, err := svc.ManualAdd(context.Background(), taskID, agentID, now, end)
	require.NoError(t, err)

	list, err := svc.ListByDay(context.Background(), agentID, now)
	require.NoError(t, err)
	assert.Len(t, list, 1)
}

// fakeInfoLookup is a stand-in TaskInfoLookup for Report tests.
// It returns exactly the map the test sets; missing ids are absent
// (matching the contract — caller renders the id slice as fallback).
type fakeInfoLookup struct {
	infos map[string]task.Info
}

func (f *fakeInfoLookup) InfosByIDs(_ context.Context, ids []string) (map[string]task.Info, error) {
	out := make(map[string]task.Info, len(ids))
	for _, id := range ids {
		if i, ok := f.infos[id]; ok {
			out[id] = i
		}
	}
	return out, nil
}

// TestTimeEntryService_Report_PopulatesInfos (Phase 27.9, T354)
// guards the one-batch lookup contract: Report enriches every row
// with the task title using a single call to InfosByIDs (no N+1),
// and missing entries fall back to an empty title (caller renders
// the id slice).
func TestTimeEntryService_Report_PopulatesInfos(t *testing.T) {
	svc, taskID, agentID, _ := setupTimeSvc(t)
	now := time.Now().Truncate(time.Second)
	end := now.Add(30 * time.Minute)
	_, err := svc.ManualAdd(context.Background(), taskID, agentID, now, end)
	require.NoError(t, err)

	from := now.Add(-time.Hour)
	to := now.Add(time.Hour)

	// Without infos wired, the row keeps an empty title (pre-27.9).
	rep, err := svc.Report(context.Background(), agentID, from, to)
	require.NoError(t, err)
	require.Len(t, rep.Tasks, 1)
	assert.Equal(t, "", rep.Tasks[0].Title, "no infos wired → empty title")

	// Wire a fake lookup and re-query — the row gets the title.
	svc.WithInfos(&fakeInfoLookup{infos: map[string]task.Info{
		taskID: {Title: "Study Redis cache invalidation"},
	}})
	rep, err = svc.Report(context.Background(), agentID, from, to)
	require.NoError(t, err)
	require.Len(t, rep.Tasks, 1)
	assert.Equal(t, "Study Redis cache invalidation", rep.Tasks[0].Title)

	// Missing lookup entry → empty title (caller renders id slice).
	svc.WithInfos(&fakeInfoLookup{})
	rep, err = svc.Report(context.Background(), agentID, from, to)
	require.NoError(t, err)
	require.Len(t, rep.Tasks, 1)
	assert.Equal(t, "", rep.Tasks[0].Title, "missing lookup key → empty title (caller falls back to id)")
}

// TestTimeEntryService_Report_GroupsByProject (T354) pins the project
// contract: task rows carry their project's id/name/color, Projects
// holds one subtotal per project sorted by total_sec desc, and a
// projectless task keeps empty project fields and stays out of
// Projects.
func TestTimeEntryService_Report_GroupsByProject(t *testing.T) {
	ctx := context.Background()
	svc, taskID, agentID, tasks, db := setupTimeSvcFull(t)
	now := time.Now().Truncate(time.Second)

	// Owner id for the second project (the fixture made exactly one).
	var ownerID string
	require.NoError(t, db.QueryRow("SELECT id FROM users LIMIT 1").Scan(&ownerID))

	projects := sqlite.NewProjectRepository(db)
	p2, _, cols2, err := projects.CreateProject(ctx, &project.Project{
		Name: "Beta", Color: "#ff0000", OwnerID: ownerID, AgentsAllowed: true,
	})
	require.NoError(t, err)
	t2 := &task.Task{ProjectID: p2.ID, ColumnID: cols2[0].ID, Title: "beta task"}
	require.NoError(t, tasks.Create(ctx, t2))
	// A projectless (Inbox) task — project_id IS NULL.
	t3 := &task.Task{Title: "orphan"}
	require.NoError(t, tasks.Create(ctx, t3))

	// Alpha task: 2 × 20m. Beta: 1h (must out-total Alpha). Orphan: 10m.
	_, err = svc.ManualAdd(ctx, taskID, agentID, now, now.Add(20*time.Minute))
	require.NoError(t, err)
	_, err = svc.ManualAdd(ctx, taskID, agentID, now.Add(time.Hour), now.Add(time.Hour).Add(20*time.Minute))
	require.NoError(t, err)
	_, err = svc.ManualAdd(ctx, t2.ID, agentID, now, now.Add(time.Hour))
	require.NoError(t, err)
	_, err = svc.ManualAdd(ctx, t3.ID, agentID, now, now.Add(10*time.Minute))
	require.NoError(t, err)

	// Real lookup path — same wiring as main.go.
	svc.WithInfos(sqlite.NewTaskRepository(db))

	rep, err := svc.Report(ctx, agentID, now.Add(-time.Hour), now.Add(2*time.Hour))
	require.NoError(t, err)

	require.Len(t, rep.Tasks, 3)
	rows := make(map[string]timeentrysvc.AggregateReportTask, len(rep.Tasks))
	for _, row := range rep.Tasks {
		rows[row.TaskID] = row
	}

	// Alpha row: joined project name comes through (fixture project
	// was created without a colour, so only the name is pinned here).
	alpha := rows[taskID]
	require.NotEmpty(t, alpha.ProjectID)
	assert.Equal(t, "TS", alpha.ProjectName)

	beta := rows[t2.ID]
	assert.Equal(t, p2.ID, beta.ProjectID)
	assert.Equal(t, "Beta", beta.ProjectName)
	assert.Equal(t, "#ff0000", beta.ProjectColor)

	// Orphan row: empty project fields.
	orphan := rows[t3.ID]
	assert.Equal(t, "", orphan.ProjectID)
	assert.Equal(t, "", orphan.ProjectName)
	assert.Equal(t, "", orphan.ProjectColor)

	// Subtotals: Beta 1h > Alpha 40m, desc order, orphan absent.
	require.Len(t, rep.Projects, 2)
	assert.Equal(t, p2.ID, rep.Projects[0].ProjectID)
	assert.Equal(t, int64(3600), rep.Projects[0].TotalSec)
	assert.Equal(t, int64(2400), rep.Projects[1].TotalSec)
	for _, p := range rep.Projects {
		assert.NotEqual(t, "", p.ProjectID, "Projects only carries real projects")
	}
	assert.Equal(t, int64(6600), rep.TotalSec)
}

// spentS reads the task's stored time_spent_s counter.
func spentS(t *testing.T, tasks task.Repository, taskID string) int {
	t.Helper()
	got, err := tasks.GetByID(context.Background(), taskID)
	require.NoError(t, err)
	return got.TimeSpentS
}

// TestTimeEntryService_StopAccruesTimeSpent (T86) pins the accounting
// contract: closing a timer adds its duration_s to tasks.time_spent_s
// in the same transaction. The service bug this guards against left
// the counter at 0 forever while time_entries filled up.
func TestTimeEntryService_StopAccruesTimeSpent(t *testing.T) {
	svc, taskID, agentID, tasks := setupTimeSvc(t)
	assert.Equal(t, 0, spentS(t, tasks, taskID), "fresh task starts at 0")

	_, err := svc.Start(context.Background(), taskID, agentID)
	require.NoError(t, err)
	time.Sleep(1100 * time.Millisecond) // >1s so duration is non-zero
	closed, err := svc.Stop(context.Background(), agentID)
	require.NoError(t, err)
	require.NotNil(t, closed.DurationS)

	assert.Equal(t, int(*closed.DurationS), spentS(t, tasks, taskID),
		"time_spent_s must grow by exactly the closed duration")

	// A second timer on the same task accumulates on top.
	_, err = svc.Start(context.Background(), taskID, agentID)
	require.NoError(t, err)
	time.Sleep(1100 * time.Millisecond)
	second, err := svc.Stop(context.Background(), agentID)
	require.NoError(t, err)
	require.NotNil(t, second.DurationS)

	assert.Equal(t, int(*closed.DurationS+*second.DurationS), spentS(t, tasks, taskID),
		"time_spent_s accumulates across timer sessions")
}

// TestTimeEntryService_ManualAddAccruesTimeSpent (T86): manual entries
// must land on tasks.time_spent_s too — POST /tasks/:id/time is the
// retrospective-import path.
func TestTimeEntryService_ManualAddAccruesTimeSpent(t *testing.T) {
	svc, taskID, agentID, tasks := setupTimeSvc(t)

	start := time.Now().Add(-time.Hour).Truncate(time.Second)
	got, err := svc.ManualAdd(context.Background(), taskID, agentID, start, start.Add(30*time.Minute))
	require.NoError(t, err)
	require.NotNil(t, got.DurationS)

	assert.Equal(t, 30*60, spentS(t, tasks, taskID),
		"manual interval must land on time_spent_s")

	// A second manual entry accumulates.
	_, err = svc.ManualAdd(context.Background(), taskID, agentID, start.Add(time.Hour), start.Add(90*time.Minute))
	require.NoError(t, err)
	assert.Equal(t, 60*60, spentS(t, tasks, taskID),
		"time_spent_s accumulates across manual entries")
}

// TestTimeEntryService_ReportAllActors (T98): an empty agentID
// aggregates entries of every actor — agent UUIDs and user ids alike —
// while a non-empty agentID still filters to that one actor.
func TestTimeEntryService_ReportAllActors(t *testing.T) {
	svc, taskID, agentID, _ := setupTimeSvc(t)
	now := time.Now().Truncate(time.Second)

	// Entry attributed to the agent.
	_, err := svc.ManualAdd(context.Background(), taskID, agentID,
		now.Add(-2*time.Hour), now.Add(-90*time.Minute))
	require.NoError(t, err)

	// Entry attributed to a user-id-shaped actor (manual entries from
	// the UI write the user's id into agent_id).
	userActor := "00000000-0000-7000-8000-0000000000aa"
	_, err = svc.ManualAdd(context.Background(), taskID, userActor,
		now.Add(-time.Hour), now.Add(-30*time.Minute))
	require.NoError(t, err)

	// Empty agentID = all actors: both entries aggregate.
	all, err := svc.Report(context.Background(), "", now.Add(-3*time.Hour), now)
	require.NoError(t, err)
	assert.Empty(t, all.AgentID)
	assert.Len(t, all.Tasks, 1)
	assert.Equal(t, int64(60*60), all.TotalSec,
		"report without agent filter must include agent + user entries")

	// Explicit agentID still scopes to one actor.
	onlyAgent, err := svc.Report(context.Background(), agentID, now.Add(-3*time.Hour), now)
	require.NoError(t, err)
	assert.Equal(t, agentID, onlyAgent.AgentID)
	assert.Len(t, onlyAgent.Tasks, 1)
	assert.Equal(t, int64(30*60), onlyAgent.TotalSec)
}
