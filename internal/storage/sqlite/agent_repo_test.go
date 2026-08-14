package sqlite

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ramgml/orenda/internal/domain/agent"
	"github.com/ramgml/orenda/internal/domain/user"
)

func setupAgentDB(t *testing.T) *sql.DB {
	t.Helper()
	dir := t.TempDir()
	db, err := Open(context.Background(), dir+"/a.db", OpenConfig{
		WALMode: true, EnableForeign: true, BusyTimeoutMs: 5000,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	require.NoError(t, Migrate(context.Background(), db, MigrationsFS, "migrations"))
	return db
}

// seedToken inserts a real user + api_tokens row and returns the token id.
// agents.token_id has a FK to api_tokens.id so we must seed it before
// inserting agents.
func seedToken(t *testing.T, db *sql.DB) string {
	t.Helper()
	users := NewUserRepository(db)
	u := &user.User{
		Email:        "u-" + t.Name() + "-" + newUUID()[:8] + "@x.com",
		PasswordHash: "x",
		DisplayName:  "Owner",
	}
	require.NoError(t, users.Create(context.Background(), u))
	tokens := NewAPITokenRepository(db)
	tok, err := tokens.Create(context.Background(), u.ID, "seed", "fakehash", "[]", nil)
	require.NoError(t, err)
	return tok.ID
}

func TestAgentRepo_CreateAndGet(t *testing.T) {
	db := setupAgentDB(t)
	tokenID := seedToken(t, db)
	repo := NewAgentRepository(db)

	a := &agent.Agent{Name: "qwen-alpha", Type: []string{"qwen"}, TokenID: tokenID}
	require.NoError(t, repo.Create(context.Background(), a))
	assert.NotEmpty(t, a.ID)
	assert.Equal(t, agent.StatusOffline, a.Status)

	got, err := repo.GetByID(context.Background(), a.ID)
	require.NoError(t, err)
	assert.Equal(t, "qwen-alpha", got.Name)
	assert.Equal(t, []string{"qwen"}, got.Type)
}

func TestAgentRepo_GetByName(t *testing.T) {
	db := setupAgentDB(t)
	tokenID := seedToken(t, db)
	repo := NewAgentRepository(db)
	require.NoError(t, repo.Create(context.Background(), &agent.Agent{Name: "a", TokenID: tokenID}))

	got, err := repo.GetByName(context.Background(), "a")
	require.NoError(t, err)
	assert.Equal(t, "a", got.Name)

	_, err = repo.GetByName(context.Background(), "no-such")
	assert.ErrorIs(t, err, agent.ErrNotFound)
}

func TestAgentRepo_DuplicateName(t *testing.T) {
	db := setupAgentDB(t)
	tokenID := seedToken(t, db)
	repo := NewAgentRepository(db)
	require.NoError(t, repo.Create(context.Background(), &agent.Agent{Name: "x", TokenID: tokenID}))
	err := repo.Create(context.Background(), &agent.Agent{Name: "x", TokenID: tokenID})
	require.Error(t, err)
	assert.ErrorIs(t, err, agent.ErrNameTaken)
}

func TestAgentRepo_TouchLastSeenAndSweepOffline(t *testing.T) {
	db := setupAgentDB(t)
	tokenID := seedToken(t, db)
	repo := NewAgentRepository(db)
	a := &agent.Agent{Name: "a", TokenID: tokenID}
	require.NoError(t, repo.Create(context.Background(), a))

	_, err := repo.TouchLastSeen(context.Background(), a.ID)
	require.NoError(t, err)
	got, err := repo.GetByID(context.Background(), a.ID)
	require.NoError(t, err)
	assert.Equal(t, agent.StatusOnline, got.Status)
	require.NotNil(t, got.LastSeenAt)

	n, err := repo.SweepOffline(context.Background(), -time.Second)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, n, int64(1))

	offline, err := repo.GetByID(context.Background(), a.ID)
	require.NoError(t, err)
	assert.Equal(t, agent.StatusOffline, offline.Status)
}

func TestAgentRepo_Delete(t *testing.T) {
	db := setupAgentDB(t)
	tokenID := seedToken(t, db)
	repo := NewAgentRepository(db)
	a := &agent.Agent{Name: "a", TokenID: tokenID}
	require.NoError(t, repo.Create(context.Background(), a))
	require.NoError(t, repo.Delete(context.Background(), a.ID))
	_, err := repo.GetByID(context.Background(), a.ID)
	assert.ErrorIs(t, err, agent.ErrNotFound)

	err = repo.Delete(context.Background(), "no-such")
	assert.ErrorIs(t, err, agent.ErrNotFound)
}

func TestAgentRepo_List(t *testing.T) {
	db := setupAgentDB(t)
	tokenID := seedToken(t, db)
	repo := NewAgentRepository(db)
	for _, n := range []string{"a", "b", "c"} {
		require.NoError(t, repo.Create(context.Background(), &agent.Agent{Name: n, TokenID: tokenID}))
	}
	got, err := repo.List(context.Background())
	require.NoError(t, err)
	assert.Len(t, got, 3)
}
