package agent_test

import (
	"context"
	"database/sql"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ramgml/orenda/internal/api/ws"
	"github.com/ramgml/orenda/internal/domain/agent"
	agentsvc "github.com/ramgml/orenda/internal/service/agent"
	"github.com/ramgml/orenda/internal/storage/sqlite"
	"github.com/ramgml/orenda/internal/testutil"
)

type recordingHub struct {
	mu     sync.Mutex
	events []recordedEvent
}

type recordedEvent struct {
	topic string
	body  any
}

func (h *recordingHub) Publish(_ context.Context, e ws.Event) {
	h.mu.Lock()
	h.events = append(h.events, recordedEvent{topic: e.Topic, body: e.Body})
	h.mu.Unlock()
}

// Close implements ws.Hub (Phase 22.3: required by Hub interface).
func (h *recordingHub) Close() {}

func (h *recordingHub) Subscribe(string, string) (<-chan ws.Event, ws.Unsubscribe) {
	ch := make(chan ws.Event, 1)
	return ch, func() { close(ch) }
}

// sqliteTokenMinter adapts sqlite.apiTokenRepo.Create to the service's
// TokenMinter interface. Returns just (id, name, err).
type sqliteTokenMinter struct {
	db *sql.DB
}

func (m *sqliteTokenMinter) MintToken(ctx context.Context, userID, name, hash, scopesJSON string, expiresAt *time.Time) (string, string, error) {
	repo := sqlite.NewAPITokenRepository(m.db)
	row, err := repo.Create(ctx, userID, name, hash, scopesJSON, expiresAt)
	if err != nil {
		return "", "", err
	}
	return row.ID, row.Name, nil
}

func setupAgentSvc(t *testing.T) (*agentsvc.Service, *recordingHub) {
	t.Helper()
	db, _ := testutil.TemplateDBOpen(t)

	users := sqlite.NewUserRepository(db)
	agents := sqlite.NewAgentRepository(db)
	hub := &recordingHub{}
	adapter := &sqliteTokenMinter{db: db}

	svc := agentsvc.New(agents, users, adapter, hub, nil)
	svc.HashCostOverride = 4
	svc.SweepTTL = 0
	return svc, hub
}

func TestService_Register(t *testing.T) {
	svc, hub := setupAgentSvc(t)

	got, err := svc.Register(context.Background(), "qwen-alpha", []string{"qwen"}, "test", []string{"tasks:read"})
	require.NoError(t, err)
	assert.NotEmpty(t, got.Agent.ID)
	assert.NotEmpty(t, got.PlainToken)
	assert.GreaterOrEqual(t, len(got.PlainToken), 32)

	assert.NotEmpty(t, hub.events, "expected agent.registered event")
	assert.Equal(t, "agents", hub.events[0].topic)
}

func TestService_Register_DuplicateName(t *testing.T) {
	svc, _ := setupAgentSvc(t)

	_, err := svc.Register(context.Background(), "dup", []string{"qwen"}, "", nil)
	require.NoError(t, err)
	_, err = svc.Register(context.Background(), "dup", []string{"claude"}, "", nil)
	require.Error(t, err)
	assert.ErrorIs(t, err, agentsvc.ErrNameTaken)
}

func TestService_Register_EmptyName(t *testing.T) {
	svc, _ := setupAgentSvc(t)
	_, err := svc.Register(context.Background(), "   ", []string{"custom"}, "", nil)
	require.Error(t, err)
}

func TestService_Heartbeat(t *testing.T) {
	svc, _ := setupAgentSvc(t)
	got, err := svc.Register(context.Background(), "hb", []string{"qwen"}, "", nil)
	require.NoError(t, err)

	hb, err := svc.Heartbeat(context.Background(), got.Agent.ID)
	require.NoError(t, err)
	assert.Equal(t, agent.StatusOnline, hb.Status)
	require.NotNil(t, hb.LastSeenAt)

	_, err = svc.Heartbeat(context.Background(), "no-such")
	assert.ErrorIs(t, err, agentsvc.ErrNotFound)
}

func TestService_SweepOffline(t *testing.T) {
	svc, _ := setupAgentSvc(t)
	got, err := svc.Register(context.Background(), "sweep", []string{"qwen"}, "", nil)
	require.NoError(t, err)

	_, err = svc.Heartbeat(context.Background(), got.Agent.ID)
	require.NoError(t, err)

	svc.SweepTTL = -1 * time.Second
	n, err := svc.SweepOffline(context.Background())
	require.NoError(t, err)
	assert.GreaterOrEqual(t, n, int64(1))
}
