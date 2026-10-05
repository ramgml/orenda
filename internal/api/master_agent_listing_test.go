package api_test

// master-agent-role plan (step 5) / Task 418: the agent task listing
// (GET /api/v1/agent/tasks, ?ready=true included) must not apply the
// Task 140 access filter to a role=master agent — it is global and
// sees every project's queue, closed projects included. The bug this
// pins: the master path left accessSet an empty map, and
// buildAgentTaskRows cut every project task against it, so a master
// agent saw only inbox rows. A project agent without a grant keeps
// the blind listing (Task 140 regression guard, unchanged).

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ramgml/orenda/internal/domain/agent"
	agentservice "github.com/ramgml/orenda/internal/service/agent"
	"github.com/ramgml/orenda/internal/storage/sqlite"
)

// mintMasterAgent registers a role=master agent on the fixture rig —
// the same agentservice.Register path newAccessFixture uses for its
// project-role agents; RequireAgent mirrors the row's role onto
// Identity.IsMaster (auth.go).
func mintMasterAgent(t *testing.T, f *accessFixture) string {
	t.Helper()
	svc := agentservice.New(f.agents, sqlite.NewUserRepository(f.db),
		&agentFixtureTMinter{tokens: sqlite.NewAPITokenRepository(f.db)}, nil, nil)
	reg, err := svc.Register(context.Background(),
		"maestro-listing-"+randLite()[:6], []string{"qwen"}, "master listing fixture", nil, agent.RoleMaster)
	require.NoError(t, err)
	require.NotEmpty(t, reg.PlainToken, "plain token is returned exactly once")
	return reg.PlainToken
}

// agentReadyRows GETs the agent task listing and returns one entry
// per row with id, title and the computed ready flag — enough to
// assert presence AND the ready semantics of ?ready=true.
func agentReadyRows(t *testing.T, f *accessFixture, token, query string) []struct {
	Task struct {
		ID    string `json:"id"`
		Title string `json:"title"`
	} `json:"task"`
	Ready bool `json:"ready"`
} {
	t.Helper()
	rr := f.do(t, http.MethodGet, "/api/v1/agent/tasks"+query, token, nil)
	require.Equal(t, http.StatusOK, rr.Code, "list tasks body=%s", rr.Body.String())
	var got struct {
		Tasks []struct {
			Task struct {
				ID    string `json:"id"`
				Title string `json:"title"`
			} `json:"task"`
			Ready bool `json:"ready"`
		} `json:"tasks"`
	}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &got))
	return got.Tasks
}

// Master bearer + closed project (agents_allowed = 0, no grant rows)
// + one todo task: ?ready=true must list it as ready. This is the
// fx/httptest proof of the master ready-queue visibility.
func TestMasterAgent_ListingReadySeesClosedProjectTask(t *testing.T) {
	t.Parallel()
	f := newAccessFixture(t)
	p := f.createProject(t, "master-closed")
	tr := f.seedTask(t, p, "master sees me")

	masterToken := mintMasterAgent(t, f)

	rows := agentReadyRows(t, f, masterToken, "?ready=true")
	var found *struct {
		Task struct {
			ID    string `json:"id"`
			Title string `json:"title"`
		} `json:"task"`
		Ready bool `json:"ready"`
	}
	for i := range rows {
		if rows[i].Task.ID == tr.ID {
			found = &rows[i]
			break
		}
	}
	require.NotNil(t, found, "master must see the closed project's todo via ?ready=true, rows=%d", len(rows))
	assert.True(t, found.Ready, "unblocked unassigned todo must be ready")
	assert.Equal(t, "master sees me", found.Task.Title)
}

// The contrast half of the contract: the same scenario under a
// project-role agent without a grant stays blind (Task 140 must not
// degrade), so the master visibility above is role-driven, not a
// filter removal for everyone.
func TestMasterAgent_ListingReady_ProjectAgentStillBlind(t *testing.T) {
	t.Parallel()
	f := newAccessFixture(t)
	p := f.createProject(t, "worker-closed")
	tr := f.seedTask(t, p, "worker must not see me")

	ids := f.agentTasks(t, f.agentAToken, "?ready=true")
	assert.NotContains(t, ids, tr.ID,
		"Task 140: an ungranted project agent must not see the closed project's task")
	assert.Empty(t, ids, "fixture agents hold no grants and there are no inbox tasks")
}
