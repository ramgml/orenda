package sqlite_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ramgml/orenda/internal/storage/sqlite"
)

func setupBackupSettingsDB(t *testing.T) (sqlite.BackupSettingsRepository, func()) {
	t.Helper()
	dir := t.TempDir()
	db, err := sqlite.Open(context.Background(), filepath.Join(dir, "bs.db"), sqlite.OpenConfig{
		WALMode: true, EnableForeign: true, BusyTimeoutMs: 5000,
	})
	require.NoError(t, err)
	require.NoError(t, sqlite.Migrate(context.Background(), db, sqlite.MigrationsFS, "migrations"))
	repo := sqlite.NewBackupSettingsRepository(db)
	return repo, func() { _ = db.Close() }
}

func TestBackupSettings_GetAll_Empty(t *testing.T) {
	repo, cleanup := setupBackupSettingsDB(t)
	defer cleanup()

	all, err := repo.GetAll(context.Background())
	require.NoError(t, err)
	assert.Empty(t, all)
}

func TestBackupSettings_SetAndGet_Roundtrip(t *testing.T) {
	repo, cleanup := setupBackupSettingsDB(t)
	defer cleanup()

	ctx := context.Background()
	// Set two distinct keys (one string, one bool-like).
	require.NoError(t, repo.SetKey(ctx, "remote_url", []byte(`"git@github.com:me/orenda.git"`)))
	require.NoError(t, repo.SetKey(ctx, "enabled", []byte(`true`)))

	// GetAll returns both, ordered by key.
	all, err := repo.GetAll(ctx)
	require.NoError(t, err)
	require.Len(t, all, 2)
	assert.Equal(t, "enabled", all[0].Key)
	assert.JSONEq(t, `true`, string(all[0].Value))
	assert.Equal(t, "remote_url", all[1].Key)
	assert.JSONEq(t, `"git@github.com:me/orenda.git"`, string(all[1].Value))

	// GetByKey — single read.
	got, ok, err := repo.GetByKey(ctx, "remote_url")
	require.NoError(t, err)
	assert.True(t, ok)
	assert.JSONEq(t, `"git@github.com:me/orenda.git"`, string(got))
}

func TestBackupSettings_GetByKey_MissingReturnsNotOK(t *testing.T) {
	repo, cleanup := setupBackupSettingsDB(t)
	defer cleanup()

	_, ok, err := repo.GetByKey(context.Background(), "nope")
	require.NoError(t, err)
	assert.False(t, ok, "missing key returns (zero, false, nil)")
}

func TestBackupSettings_GetByKey_EmptyKeyRejected(t *testing.T) {
	repo, cleanup := setupBackupSettingsDB(t)
	defer cleanup()

	_, ok, err := repo.GetByKey(context.Background(), "")
	assert.False(t, ok)
	require.ErrorIs(t, err, sqlite.ErrInvalidSettingKey)
}

func TestBackupSettings_UpdateIsUpsert(t *testing.T) {
	repo, cleanup := setupBackupSettingsDB(t)
	defer cleanup()

	ctx := context.Background()
	require.NoError(t, repo.SetKey(ctx, "remote_url", []byte(`"https://x/y.git"`)))
	// Second Set on the same key replaces — not duplicate row.
	require.NoError(t, repo.SetKey(ctx, "remote_url", []byte(`"https://z/w.git"`)))

	all, err := repo.GetAll(ctx)
	require.NoError(t, err)
	require.Len(t, all, 1, "SetKey is upsert, not append")
	assert.JSONEq(t, `"https://z/w.git"`, string(all[0].Value))
}

func TestBackupSettings_SetKey_RejectsInvalidJSON(t *testing.T) {
	repo, cleanup := setupBackupSettingsDB(t)
	defer cleanup()

	err := repo.SetKey(context.Background(), "remote_url", []byte(`{not-json`))
	require.ErrorIs(t, err, sqlite.ErrInvalidSettingValue)
}

func TestBackupSettings_SetKey_RejectsEmptyKey(t *testing.T) {
	repo, cleanup := setupBackupSettingsDB(t)
	defer cleanup()

	err := repo.SetKey(context.Background(), "", []byte(`"x"`))
	require.ErrorIs(t, err, sqlite.ErrInvalidSettingKey)
}

func TestBackupSettings_ClearByKey_RemovesRow(t *testing.T) {
	repo, cleanup := setupBackupSettingsDB(t)
	defer cleanup()

	ctx := context.Background()
	require.NoError(t, repo.SetKey(ctx, "remote_url", []byte(`"x"`)))

	// Clear removes the row entirely — GetByKey afterwards reports
	// "not set", so callers can fall back to the cfg default.
	require.NoError(t, repo.ClearByKey(ctx, "remote_url"))

	_, ok, err := repo.GetByKey(ctx, "remote_url")
	require.NoError(t, err)
	assert.False(t, ok)

	all, err := repo.GetAll(ctx)
	require.NoError(t, err)
	assert.Empty(t, all)
}
