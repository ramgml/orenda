package backup_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ramgml/orenda/internal/backup"
	"github.com/ramgml/orenda/internal/storage/sqlite"
)

// writeUser seeds a row into users via the given db handle.
func writeUser(t *testing.T, dbPath string) {
	t.Helper()
	db, err := sqlite.Open(context.Background(), dbPath, sqlite.OpenConfig{
		WALMode: false, EnableForeign: true, BusyTimeoutMs: 1000,
	})
	require.NoError(t, err)
	defer db.Close()
	_, err = db.ExecContext(context.Background(),
		`INSERT INTO users (id, email, password_hash, display_name, role) VALUES (?, ?, ?, ?, ?)`,
		"u-1", "a@b.c", "x", "Alice", "owner")
	require.NoError(t, err)
}

// countUsers returns the users row count for a sqlite db at dbPath.
func countUsers(t *testing.T, dbPath string) int {
	t.Helper()
	db, err := sqlite.Open(context.Background(), dbPath, sqlite.OpenConfig{
		WALMode: false, EnableForeign: true, BusyTimeoutMs: 1000,
	})
	require.NoError(t, err)
	defer db.Close()
	var n int
	require.NoError(t, db.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM users`).Scan(&n))
	return n
}

// TestRestore_OverwritesDestFromSnapshot: snapshot has 1 user row,
// destination starts empty, after Restore destination has 1 user row.
func TestRestore_OverwritesDestFromSnapshot(t *testing.T) {
	db, dbPath := setupDB(t)
	dir := t.TempDir()
	svc := backup.New(backup.Config{
		SnapshotDir: filepath.Join(dir, "snap"),
		DBPath:      dbPath,
	}, db)

	writeUser(t, dbPath)
	snapPath, err := svc.Snapshot(context.Background())
	require.NoError(t, err)

	// Build a fresh destination DB that is empty (no user row).
	dst := filepath.Join(dir, "restored.db")
	dstDB, err := sqlite.Open(context.Background(), dst, sqlite.OpenConfig{
		WALMode: false, EnableForeign: true, BusyTimeoutMs: 1000,
	})
	require.NoError(t, err)
	require.NoError(t, sqlite.Migrate(context.Background(), dstDB, sqlite.MigrationsFS, "migrations"))
	require.NoError(t, dstDB.Close())
	assert.Equal(t, 0, countUsers(t, dst))

	require.NoError(t, svc.Restore(context.Background(), snapPath, dst))
	assert.Equal(t, 1, countUsers(t, dst))
}

// TestRestore_RemovesStaleSidecars: a pre-existing -wal/-shm next to the
// destination must be removed after Restore.
func TestRestore_RemovesStaleSidecars(t *testing.T) {
	db, dbPath := setupDB(t)
	dir := t.TempDir()
	svc := backup.New(backup.Config{
		SnapshotDir: filepath.Join(dir, "snap"),
		DBPath:      dbPath,
	}, db)
	snapPath, err := svc.Snapshot(context.Background())
	require.NoError(t, err)

	dst := filepath.Join(dir, "dst.db")
	require.NoError(t, os.WriteFile(dst, []byte("placeholder"), 0o644))
	require.NoError(t, os.WriteFile(dst+"-wal", []byte("wal"), 0o644))
	require.NoError(t, os.WriteFile(dst+"-shm", []byte("shm"), 0o644))

	require.NoError(t, svc.Restore(context.Background(), snapPath, dst))
	assert.NoFileExists(t, dst+"-wal")
	assert.NoFileExists(t, dst+"-shm")
	assert.FileExists(t, dst)
}

// TestRestore_RefusesNonSQLite: passing a file that does not start with
// the sqlite magic header must fail with ErrNotSQLite.
func TestRestore_RefusesNonSQLite(t *testing.T) {
	db, dbPath := setupDB(t)
	dir := t.TempDir()
	svc := backup.New(backup.Config{SnapshotDir: filepath.Join(dir, "snap"), DBPath: dbPath}, db)

	bad := filepath.Join(dir, "notsqlite.bin")
	require.NoError(t, os.WriteFile(bad, []byte("hello world, this is not sqlite"), 0o644))
	dst := filepath.Join(dir, "dst.db")

	err := svc.Restore(context.Background(), bad, dst)
	require.Error(t, err)
	assert.True(t, errors.Is(err, backup.ErrNotSQLite), "expected ErrNotSQLite, got %v", err)
	assert.NoFileExists(t, dst)
}

// TestRestore_RejectsMissingSnapshot: ErrNotFound when the snapshot path
// does not exist.
func TestRestore_RejectsMissingSnapshot(t *testing.T) {
	db, dbPath := setupDB(t)
	dir := t.TempDir()
	svc := backup.New(backup.Config{SnapshotDir: filepath.Join(dir, "snap"), DBPath: dbPath}, db)

	err := svc.Restore(context.Background(), filepath.Join(dir, "no-such.db"), filepath.Join(dir, "dst.db"))
	require.Error(t, err)
	assert.True(t, errors.Is(err, backup.ErrNotFound))
}

// TestRestore_RejectsEmptyArgs: empty inputs → ErrInvalidInput.
func TestRestore_RejectsEmptyArgs(t *testing.T) {
	db, dbPath := setupDB(t)
	svc := backup.New(backup.Config{DBPath: dbPath}, db)

	assert.True(t, errors.Is(svc.Restore(context.Background(), "", "/tmp/x"), backup.ErrInvalidInput))
	assert.True(t, errors.Is(svc.Restore(context.Background(), "/tmp/x", ""), backup.ErrInvalidInput))
}

// TestRestore_IsAtomicNoTmpLeftBehind: after a successful Restore, no
// ".restore.tmp" sidecar is left in the destination directory.
func TestRestore_IsAtomicNoTmpLeftBehind(t *testing.T) {
	db, dbPath := setupDB(t)
	dir := t.TempDir()
	svc := backup.New(backup.Config{SnapshotDir: filepath.Join(dir, "snap"), DBPath: dbPath}, db)
	snapPath, err := svc.Snapshot(context.Background())
	require.NoError(t, err)

	dst := filepath.Join(dir, "dst.db")
	require.NoError(t, svc.Restore(context.Background(), snapPath, dst))

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	for _, e := range entries {
		assert.False(t, filepath.Ext(e.Name()) == ".tmp", "no .tmp should remain, got %s", e.Name())
	}
}

// TestIsServerRunning: dial an arbitrary free port → false; close it →
// also false (no listener). The "true" path is exercised in the CLI
// integration test (since we'd need to spin up a real listener here).
func TestIsServerRunning_FreePort(t *testing.T) {
	// Port 1 is reserved/unused on Linux; any connect attempt fails fast.
	assert.False(t, backup.IsServerRunning(context.Background(), "127.0.0.1", 1))
	// Bogus host: also false.
	assert.False(t, backup.IsServerRunning(context.Background(), "127.0.0.1.invalid", 2137))
	// Negative / zero port: returns false without dialing.
	assert.False(t, backup.IsServerRunning(context.Background(), "127.0.0.1", 0))
}

// Sanity: Service.Restore respects the ctx (we don't actually cancel
// mid-call here, but the signature is wired).
var _ = time.Second
