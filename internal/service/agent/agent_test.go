package agent_test

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ramgml/orenda/internal/api/ws"
	"github.com/ramgml/orenda/internal/auth"
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

func (m *sqliteTokenMinter) UpdateHash(ctx context.Context, tokenID, hash string) error {
	return sqlite.NewAPITokenRepository(m.db).UpdateHash(ctx, tokenID, hash)
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

// setupAgentSvcWithDB is setupAgentSvc for tests that also need the raw
// DB handle (e.g. asserting on the users table directly).
func setupAgentSvcWithDB(t *testing.T) (*agentsvc.Service, *sql.DB) {
	t.Helper()
	db, _ := testutil.TemplateDBOpen(t)

	users := sqlite.NewUserRepository(db)
	agents := sqlite.NewAgentRepository(db)
	adapter := &sqliteTokenMinter{db: db}

	svc := agentsvc.New(agents, users, adapter, &recordingHub{}, nil)
	svc.HashCostOverride = 4
	svc.SweepTTL = 0
	return svc, db
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

// tokenRowByTokenID fetches the raw api_tokens row for an agent's
// token — lets the rotation tests inspect the stored hash directly.
func tokenRowByTokenID(t *testing.T, db *sql.DB, tokenID string) sqlite.StoredToken {
	t.Helper()
	row, err := sqlite.NewAPITokenRepository(db).GetByID(context.Background(), tokenID)
	require.NoError(t, err)
	return *row
}

func TestService_RotateToken(t *testing.T) {
	svc, _ := setupAgentSvc(t)
	ctx := context.Background()
	reg, err := svc.Register(ctx, "rot", []string{"qwen"}, "", []string{"tasks:read"})
	require.NoError(t, err)

	db := svc.Tokens.(*sqliteTokenMinter).db
	before := tokenRowByTokenID(t, db, reg.Agent.TokenID)

	rot, err := svc.RotateToken(ctx, reg.Agent.ID)
	require.NoError(t, err)

	// New plaintext minted, distinct from the old one.
	assert.NotEmpty(t, rot.PlainToken)
	assert.NotEqual(t, reg.PlainToken, rot.PlainToken)

	// Same api_tokens row (agent identity survives), new hash.
	assert.Equal(t, reg.Agent.TokenID, rot.Agent.TokenID)
	after := tokenRowByTokenID(t, db, rot.Agent.TokenID)
	assert.Equal(t, before.ID, after.ID)
	assert.Equal(t, before.UserID, after.UserID)
	assert.Equal(t, before.ScopesJSON, after.ScopesJSON)
	assert.NotEqual(t, before.Hash, after.Hash)

	// The old plaintext no longer verifies against the stored hash;
	// the new one does.
	assert.Error(t, auth.VerifyAPIToken(after.Hash, reg.PlainToken))
	assert.NoError(t, auth.VerifyAPIToken(after.Hash, rot.PlainToken))

	// The agent row is untouched (same id/name/labels/settings).
	fresh, err := svc.Agents.GetByID(ctx, reg.Agent.ID)
	require.NoError(t, err)
	assert.Equal(t, reg.Agent.ID, fresh.ID)
	assert.Equal(t, reg.Agent.Name, fresh.Name)
	assert.Equal(t, reg.Agent.Type, fresh.Type)
	assert.Equal(t, reg.Agent.Description, fresh.Description)
	assert.Equal(t, reg.Agent.TokenID, fresh.TokenID)
	assert.Equal(t, reg.Agent.MaxConcurrent, fresh.MaxConcurrent)
}

func TestService_RotateToken_PublishesEvent(t *testing.T) {
	svc, hub := setupAgentSvc(t)
	reg, err := svc.Register(context.Background(), "rot-ev", []string{"qwen"}, "", nil)
	require.NoError(t, err)

	_, err = svc.RotateToken(context.Background(), reg.Agent.ID)
	require.NoError(t, err)

	last := hub.events[len(hub.events)-1]
	assert.Equal(t, "agents", last.topic)
	assert.Equal(t, "agent.token_rotated", last.body.(map[string]any)["type"])
}

func TestService_RotateToken_NotFound(t *testing.T) {
	svc, _ := setupAgentSvc(t)
	_, err := svc.RotateToken(context.Background(), "no-such-agent")
	assert.ErrorIs(t, err, agentsvc.ErrNotFound)
}

// UpdateHash failure must leave the old hash intact — the agent
// keeps working with the old token after a failed rotation
// (transactionality of the credential swap).
func TestService_RotateToken_UpdateHashErrorLeavesOldHash(t *testing.T) {
	ctx := context.Background()
	db, _ := testutil.TemplateDBOpen(t)

	users := sqlite.NewUserRepository(db)
	agentsRepo := sqlite.NewAgentRepository(db)
	svc := agentsvc.New(agentsRepo, users, &failingMinter{db: db}, nil, nil)
	svc.HashCostOverride = 4

	reg, err := svc.Register(ctx, "rot-fail", []string{"qwen"}, "", nil)
	require.NoError(t, err)

	before := tokenRowByTokenID(t, db, reg.Agent.TokenID)

	_, err = svc.RotateToken(ctx, reg.Agent.ID)
	require.Error(t, err)

	after := tokenRowByTokenID(t, db, reg.Agent.TokenID)
	assert.Equal(t, before.Hash, after.Hash, "failed rotation must not touch the stored hash")
	assert.NoError(t, auth.VerifyAPIToken(after.Hash, reg.PlainToken), "old token still works")
}

// failingMinter wraps the real sqlite-backed minter but fails every
// UpdateHash — simulates a storage error mid-rotation.
type failingMinter struct {
	db *sql.DB
}

func (m *failingMinter) MintToken(ctx context.Context, userID, name, hash, scopesJSON string, expiresAt *time.Time) (string, string, error) {
	return (&sqliteTokenMinter{db: m.db}).MintToken(ctx, userID, name, hash, scopesJSON, expiresAt)
}

func (m *failingMinter) UpdateHash(ctx context.Context, tokenID, hash string) error {
	return errors.New("injected UpdateHash failure")
// TestService_Register_SyntheticOwnerIsSystemRole pins the T171 hardening:
// the synthetic agent-owner user that ensureOwner lazily creates on a
// fresh database must carry role='system', NOT the Validate()'s
// RoleOwner default. Before T171 the struct literal set no role, so the
// constant "unusable" password bought a full owner session via
// loginHandler.
func TestService_Register_SyntheticOwnerIsSystemRole(t *testing.T) {
	svc, db := setupAgentSvcWithDB(t)
	ctx := context.Background()

	_, err := svc.Register(ctx, "role-probe", []string{"qwen"}, "", nil)
	require.NoError(t, err, "Register must create the synthetic owner on a fresh DB")

	var role string
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT role FROM users WHERE email = 'agent-owner@orenda.local'`).Scan(&role))
	assert.Equal(t, "system", role, "synthetic agent-owner must be created with role='system' in the DB")
}

// TestService_EnsureOwner_RoleStableOnLookup pins the second half of the
// ensureOwner contract: when the row already exists (any role), the
// service must return it as-is — no rewrite, no second normalize
// mechanism (healing legacy rows is migration 044's job, and login is
// gated in loginHandler).
func TestService_EnsureOwner_RoleStableOnLookup(t *testing.T) {
	svc, db := setupAgentSvcWithDB(t)
	ctx := context.Background()

	_, err := svc.Register(ctx, "first", []string{"qwen"}, "", nil)
	require.NoError(t, err)
	_, err = svc.Register(ctx, "second", []string{"qwen"}, "", nil)
	require.NoError(t, err, "second Register must reuse, not recreate, the synthetic owner")

	var n int
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT count(*) FROM users WHERE email = 'agent-owner@orenda.local'`).Scan(&n))
	assert.Equal(t, 1, n, "exactly one synthetic owner row must exist")
}
