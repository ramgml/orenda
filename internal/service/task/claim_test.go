package task_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ramgml/orenda/internal/api/ws"
	"github.com/ramgml/orenda/internal/domain/agent"
	"github.com/ramgml/orenda/internal/domain/project"
	"github.com/ramgml/orenda/internal/domain/task"
	"github.com/ramgml/orenda/internal/domain/user"
	taskservice "github.com/ramgml/orenda/internal/service/task"
	"github.com/ramgml/orenda/internal/storage/sqlite"
)

type claimHub struct {
	mu     sync.Mutex
	events []claimRecorded
}

type claimRecorded struct {
	topic string
	body  any
}

func (h *claimHub) Publish(_ context.Context, e ws.Event) {
	h.mu.Lock()
	h.events = append(h.events, claimRecorded{topic: e.Topic, body: e.Body})
	h.mu.Unlock()
}

// Close implements ws.Hub (Phase 22.3).
func (h *claimHub) Close() {}

func (h *claimHub) Subscribe(string, string) (<-chan ws.Event, ws.Unsubscribe) {
	ch := make(chan ws.Event, 1)
	return ch, func() { close(ch) }
}

func setupClaimDB(t *testing.T) (*sql.DB, *taskservice.Service, *claimHub, *project.Project, *project.Column) {
	t.Helper()
	dir := t.TempDir()
	db, err := sqlite.Open(context.Background(), filepath.Join(dir+"/t.db"), sqlite.OpenConfig{
		WALMode: true, EnableForeign: true, BusyTimeoutMs: 5000,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	require.NoError(t, sqlite.Migrate(context.Background(), db, sqlite.MigrationsFS, "migrations"))

	users := sqlite.NewUserRepository(db)
	owner := &user.User{
		Email:        "cl-owner-" + strings.ReplaceAll(t.Name(), "/", "-") + "-" + newUUIDLite()[:8] + "@x.com",
		PasswordHash: "x",
		DisplayName:  "O",
	}
	require.NoError(t, users.Create(context.Background(), owner))

	projRepo := sqlite.NewProjectRepository(db)
	p, _, cols, err := projRepo.CreateProject(context.Background(), &project.Project{
		Name: "Orenda", OwnerID: owner.ID,
	})
	require.NoError(t, err)

	tasks := sqlite.NewTaskRepository(db)
	tr := &task.Task{ProjectID: p.ID, ColumnID: cols[0].ID, Title: "x"}
	require.NoError(t, tasks.Create(context.Background(), tr))

	hub := &claimHub{}
	svc := taskservice.New(
		tasks,
		sqlite.NewTaskLockRepository(db),
		nil, // recorder; Phase 3.9
		nil, // comments; Phase 3.7
		hub,
	)
	return db, svc, hub, p, cols[0]
}

// seedAgent inserts an agent row needed for task_locks.agent_id FK.
// Returns the agent id. The token_id is fake — task_locks only references
// agents.id, not the api_tokens table.
func seedAgent(t *testing.T, db *sql.DB, label string) string {
	t.Helper()
	tokens := sqlite.NewAPITokenRepository(db)
	users := sqlite.NewUserRepository(db)
	u := &user.User{Email: "seed-" + label + "-" + newUUIDLite()[:8] + "@x.com", PasswordHash: "x", DisplayName: "O"}
	require.NoError(t, users.Create(context.Background(), u))
	tok, err := tokens.Create(context.Background(), u.ID, "tok-"+label, "fake", "[]", nil)
	require.NoError(t, err)
	agents := sqlite.NewAgentRepository(db)
	a := &agent.Agent{Name: "seed-" + label + "-" + newUUIDLite()[:8], Type: []string{"qwen"}, TokenID: tok.ID}
	require.NoError(t, agents.Create(context.Background(), a))
	return a.ID
}

func TestService_Claim(t *testing.T) {
	_, svc, hub, _, _ := setupClaimDB(t)

	// Need an agent row first (FK on task_locks.agent_id is to agents.id).
	// Actually — task_locks.agent_id has no FK in 001_init, so we can use
	// any string.
	moved, err := svc.Claim(context.Background(), "fake-task-id", "a-1")
	require.Error(t, err, "expected ErrNotFound since task doesn't exist")
	assert.Nil(t, moved)
	_ = hub
}

func TestService_Claim_Success(t *testing.T) {
	db, svc, hub, p, col := setupClaimDB(t)

	tasks := sqlite.NewTaskRepository(db)
	tr := &task.Task{ProjectID: p.ID, ColumnID: col.ID, Title: "x"}
	require.NoError(t, tasks.Create(context.Background(), tr))

	agentID := seedAgent(t, db, "claim1")
	claimed, err := svc.Claim(context.Background(), tr.ID, agentID)
	require.NoError(t, err)
	require.NotNil(t, claimed)
	assert.Equal(t, task.StatusInProgress, claimed.Status)
	assert.Equal(t, agentID, claimed.AssigneeID)
	assert.Equal(t, task.AssigneeAgent, claimed.AssigneeType)
	require.NotNil(t, claimed.ClaimedAt)

	assert.NotEmpty(t, hub.events, "expected task.claimed event")
}

func TestService_Claim_ConcurrentOnlyOneWins(t *testing.T) {
	db, _, _, _, _ := setupClaimDB(t)
	users := sqlite.NewUserRepository(db)
	owner := &user.User{Email: "conc-" + newUUIDLite()[:8] + "@x.com", PasswordHash: "x", DisplayName: "O"}
	require.NoError(t, users.Create(context.Background(), owner))
	projRepo := sqlite.NewProjectRepository(db)
	p, _, cols, err := projRepo.CreateProject(context.Background(), &project.Project{Name: "R", OwnerID: owner.ID})
	require.NoError(t, err)
	tasks := sqlite.NewTaskRepository(db)
	tr := &task.Task{ProjectID: p.ID, ColumnID: cols[0].ID, Title: "x"}
	require.NoError(t, tasks.Create(context.Background(), tr))

	agent1 := seedAgent(t, db, "agent1")
	agent2 := seedAgent(t, db, "agent2")

	locks := sqlite.NewTaskLockRepository(db)
	hub := &claimHub{}
	svc1 := taskservice.New(tasks, locks, nil, nil, hub)
	svc2 := taskservice.New(tasks, locks, nil, nil, hub)

	var wg sync.WaitGroup
	var s1Err, s2Err error
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, s1Err = svc1.Claim(context.Background(), tr.ID, agent1)
	}()
	go func() {
		defer wg.Done()
		_, s2Err = svc2.Claim(context.Background(), tr.ID, agent2)
	}()
	wg.Wait()

	successes := 0
	failures := 0
	for _, e := range []error{s1Err, s2Err} {
		if e == nil {
			successes++
		} else if assert.ErrorIs(t, e, taskservice.ErrLockTaken) {
			failures++
		}
	}
	assert.Equal(t, 1, successes, "exactly one Claim must succeed")
	assert.Equal(t, 1, failures, "the other must fail with ErrLockTaken")
}

func TestService_Release(t *testing.T) {
	db, svc, _, _, _ := setupClaimDB(t)
	users := sqlite.NewUserRepository(db)
	owner := &user.User{Email: "rel-" + newUUIDLite()[:8] + "@x.com", PasswordHash: "x", DisplayName: "O"}
	require.NoError(t, users.Create(context.Background(), owner))
	projRepo := sqlite.NewProjectRepository(db)
	p, _, cols, _ := projRepo.CreateProject(context.Background(), &project.Project{Name: "R", OwnerID: owner.ID})
	tasks := sqlite.NewTaskRepository(db)
	tr := &task.Task{ProjectID: p.ID, ColumnID: cols[0].ID, Title: "x"}
	require.NoError(t, tasks.Create(context.Background(), tr))

	agentID := seedAgent(t, db, "agent1")
	_, err := svc.Claim(context.Background(), tr.ID, agentID)
	require.NoError(t, err)

	released, err := svc.Release(context.Background(), tr.ID, agentID)
	require.NoError(t, err)
	assert.Equal(t, task.StatusTodo, released.Status)
	assert.Empty(t, released.AssigneeID)
}

func TestService_Release_NotHeld(t *testing.T) {
	db, svc, _, _, _ := setupClaimDB(t)
	_, err := svc.Release(context.Background(), "no-such-task", seedAgent(t, db, "agent1"))
	require.Error(t, err)
}

func TestService_Submit(t *testing.T) {
	db, svc, hub, _, _ := setupClaimDB(t)
	users := sqlite.NewUserRepository(db)
	owner := &user.User{Email: "sub-" + newUUIDLite()[:8] + "@x.com", PasswordHash: "x", DisplayName: "O"}
	require.NoError(t, users.Create(context.Background(), owner))
	projRepo := sqlite.NewProjectRepository(db)
	p, _, cols, _ := projRepo.CreateProject(context.Background(), &project.Project{Name: "S", OwnerID: owner.ID})
	tasks := sqlite.NewTaskRepository(db)
	tr := &task.Task{ProjectID: p.ID, ColumnID: cols[0].ID, Title: "x"}
	require.NoError(t, tasks.Create(context.Background(), tr))

	submitted, err := svc.Submit(context.Background(), tr.ID, seedAgent(t, db, "agent1"), "ready for review")
	require.NoError(t, err)
	assert.Equal(t, task.StatusReview, submitted.Status)
	assert.Equal(t, task.AwaitingHuman, submitted.Awaiting)
	assert.Equal(t, "ready for review", submitted.AgentNotes)

	assert.NotEmpty(t, hub.events)
}

func TestService_Review_Approve(t *testing.T) {
	db, svc, _, _, _ := setupClaimDB(t)
	users := sqlite.NewUserRepository(db)
	owner := &user.User{Email: "rev-" + newUUIDLite()[:8] + "@x.com", PasswordHash: "x", DisplayName: "O"}
	require.NoError(t, users.Create(context.Background(), owner))
	projRepo := sqlite.NewProjectRepository(db)
	p, _, cols, _ := projRepo.CreateProject(context.Background(), &project.Project{Name: "R", OwnerID: owner.ID})
	tasks := sqlite.NewTaskRepository(db)
	tr := &task.Task{ProjectID: p.ID, ColumnID: cols[0].ID, Title: "x"}
	require.NoError(t, tasks.Create(context.Background(), tr))

	_, err := svc.Submit(context.Background(), tr.ID, seedAgent(t, db, "agent1"), "")
	require.NoError(t, err)

	reviewed, err := svc.Review(context.Background(), tr.ID, owner.ID, taskservice.ReviewApprove, "")
	require.NoError(t, err)
	assert.Equal(t, task.StatusDone, reviewed.Status)
	assert.Equal(t, task.AwaitingNone, reviewed.Awaiting)
	require.NotNil(t, reviewed.CompletedAt)
}

func TestService_Review_Reject(t *testing.T) {
	db, svc, _, _, _ := setupClaimDB(t)
	users := sqlite.NewUserRepository(db)
	owner := &user.User{Email: "rev2-" + newUUIDLite()[:8] + "@x.com", PasswordHash: "x", DisplayName: "O"}
	require.NoError(t, users.Create(context.Background(), owner))
	projRepo := sqlite.NewProjectRepository(db)
	p, _, cols, _ := projRepo.CreateProject(context.Background(), &project.Project{Name: "R2", OwnerID: owner.ID})
	tasks := sqlite.NewTaskRepository(db)
	tr := &task.Task{ProjectID: p.ID, ColumnID: cols[0].ID, Title: "x"}
	require.NoError(t, tasks.Create(context.Background(), tr))
	_, err := svc.Submit(context.Background(), tr.ID, seedAgent(t, db, "agent1"), "")
	require.NoError(t, err)

	rejected, err := svc.Review(context.Background(), tr.ID, owner.ID, taskservice.ReviewReject, "fix tests")
	require.NoError(t, err)
	assert.Equal(t, task.StatusInProgress, rejected.Status)
	assert.Equal(t, task.AwaitingAgent, rejected.Awaiting)
}

func TestService_Review_InvalidDecision(t *testing.T) {
	_, svc, _, _, _ := setupClaimDB(t)
	_, err := svc.Review(context.Background(), "x", "u", taskservice.ReviewDecision("bogus"), "")
	require.Error(t, err)
	assert.ErrorIs(t, err, taskservice.ErrInvalidInput)
}

var _ = agent.StatusOnline // keep import
