package backup_test

import (
	"context"
	"database/sql"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ramgml/orenda/internal/backup"
	"github.com/ramgml/orenda/internal/storage/sqlite"
)

func setupDB(t *testing.T) (*sql.DB, string) {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "orenda.db")
	db, err := sqlite.Open(context.Background(), dbPath, sqlite.OpenConfig{
		WALMode: true, EnableForeign: true, BusyTimeoutMs: 5000,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	require.NoError(t, sqlite.Migrate(context.Background(), db, sqlite.MigrationsFS, "migrations"))
	return db, dbPath
}

func TestBackup_SnapshotCreatesFile(t *testing.T) {
	db, dbPath := setupDB(t)
	dir := t.TempDir()
	svc := backup.New(backup.Config{
		MirrorDir:            filepath.Join(dir, "mirror"),
		SnapshotDir:          filepath.Join(dir, "snapshots"),
		DBPath:               dbPath,
		SnapshotRotationDays: 30,
	}, db)

	path, err := svc.Snapshot(context.Background())
	require.NoError(t, err)
	assert.FileExists(t, path)
	assert.True(t, strings.HasSuffix(path, ".db"))
}

func TestBackup_SnapshotIsQueryable(t *testing.T) {
	db, dbPath := setupDB(t)
	dir := t.TempDir()
	svc := backup.New(backup.Config{
		SnapshotDir: filepath.Join(dir, "snapshots"),
		DBPath:      dbPath,
	}, db)

	path, err := svc.Snapshot(context.Background())
	require.NoError(t, err)

	// The snapshot is a valid SQLite database.
	snap, err := sqlite.Open(context.Background(), path, sqlite.OpenConfig{
		WALMode: false, EnableForeign: true, BusyTimeoutMs: 1000,
	})
	require.NoError(t, err)
	defer snap.Close()
	var n int
	require.NoError(t, snap.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='users'`).Scan(&n))
	assert.Equal(t, 1, n)
}

func TestBackup_ListSnapshots(t *testing.T) {
	db, dbPath := setupDB(t)
	dir := t.TempDir()
	svc := backup.New(backup.Config{
		SnapshotDir: filepath.Join(dir, "snapshots"),
		DBPath:      dbPath,
	}, db)

	for i := 0; i < 3; i++ {
		_, err := svc.Snapshot(context.Background())
		require.NoError(t, err)
		time.Sleep(1100 * time.Millisecond) // ensure unique timestamps
	}

	list, err := svc.ListSnapshots(context.Background())
	require.NoError(t, err)
	assert.Len(t, list, 3)
	// Newest first.
	for i := 0; i < 2; i++ {
		assert.True(t, list[i].ModTime.After(list[i+1].ModTime) ||
			list[i].ModTime.Equal(list[i+1].ModTime))
	}
}

func TestBackup_SnapshotRotation(t *testing.T) {
	db, dbPath := setupDB(t)
	dir := t.TempDir()
	svc := backup.New(backup.Config{
		SnapshotDir:          filepath.Join(dir, "snapshots"),
		DBPath:               dbPath,
		SnapshotRotationDays: 0, // disabled
	}, db)
	_, err := svc.Snapshot(context.Background())
	require.NoError(t, err)
	// Pretend the snapshot is 60 days old.
	list, _ := svc.ListSnapshots(context.Background())
	require.Len(t, list, 1)
	old := time.Now().AddDate(0, 0, -60)
	_ = os.Chtimes(list[0].Path, old, old)

	// Enable rotation at 30 days; a new Snapshot call should drop the old one.
	svc30 := backup.New(backup.Config{
		SnapshotDir:          filepath.Join(dir, "snapshots"),
		DBPath:               dbPath,
		SnapshotRotationDays: 30,
	}, db)
	_, err = svc30.Snapshot(context.Background())
	require.NoError(t, err)

	after, err := svc30.ListSnapshots(context.Background())
	require.NoError(t, err)
	// Should only have the newest one (the 60-day-old was rotated out).
	assert.Len(t, after, 1)
}

func TestBackup_RecordAndListLog(t *testing.T) {
	db, dbPath := setupDB(t)
	svc := backup.New(backup.Config{DBPath: dbPath}, db)

	require.NoError(t, svc.RecordLog(context.Background(),
		"sqlite_snapshot", "success", "first", "/tmp/x.db"))
	require.NoError(t, svc.RecordLog(context.Background(),
		"git_push", "failed", "network error", ""))

	list, err := svc.ListLog(context.Background(), 10)
	require.NoError(t, err)
	require.Len(t, list, 2)
	assert.Equal(t, "git_push", list[0].Type) // newest first
	assert.Equal(t, "sqlite_snapshot", list[1].Type)
}

// TestBackup_GitPushToBareRepo verifies the push path against a local
// bare repo. Skipped when `git` isn't on PATH.
func TestBackup_GitPushToBareRepo(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	db, dbPath := setupDB(t)
	dir := t.TempDir()

	// Create a bare repo to push to.
	bare := filepath.Join(dir, "remote.git")
	require.NoError(t, exec.Command("git", "init", "--bare", bare).Run())

	mirrorDir := filepath.Join(dir, "mirror")
	require.NoError(t, os.MkdirAll(mirrorDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(mirrorDir, "test.md"), []byte("# hello"), 0o644))

	svc := backup.New(backup.Config{
		MirrorDir: mirrorDir,
		RemoteURL: bare,
		DBPath:    dbPath,
	}, db)
	require.NoError(t, svc.CommitAndPush(context.Background(), "test push"))

	// Verify the remote got the commit.
	out, err := exec.Command("git", "--git-dir", bare, "log", "--oneline", "HEAD").CombinedOutput()
	require.NoError(t, err)
	assert.Contains(t, string(out), "test push")
}

func TestBackup_CommitAndPush_NoRemote(t *testing.T) {
	db, dbPath := setupDB(t)
	dir := t.TempDir()
	svc := backup.New(backup.Config{
		MirrorDir: filepath.Join(dir, "mirror"),
		DBPath:    dbPath,
	}, db)
	err := svc.CommitAndPush(context.Background(), "x")
	assert.ErrorIs(t, err, backup.ErrNoRemote)
}

// Phase 28.9 (polish): hot-reload via atomic.Pointer[Config]. The
// PUT /api/v1/backups/settings handler calls Service.UpdateConfig
// after persisting to the backup_settings table. Three contracts
// worth pinning:
//
//  1. UpdateConfig is atomic — concurrent readers always see
//     either the old or the new Config, never a torn value.
//  2. Config() returns a defensive copy — mutating the
//     returned struct must not silently change Service state.
//  3. -race detector finds no unsynchronised access.
func TestService_UpdateConfig_SwapsConfigAtomically(t *testing.T) {
	db, dbPath := setupDB(t)
	initial := backup.Config{
		MirrorDir: t.TempDir(),
		DBPath:    dbPath,
		RemoteURL: "git@github.com:old/repo.git",
	}
	svc := backup.New(initial, db)
	got := svc.Config()
	assert.Equal(t, "git@github.com:old/repo.git", got.RemoteURL,
		"Service starts with the constructor-passed config")

	// Hot-reload — point at a new remote without restarting.
	svc.UpdateConfig(backup.Config{
		MirrorDir: initial.MirrorDir,
		DBPath:    initial.DBPath,
		RemoteURL: "git@github.com:new/repo.git",
	})
	got = svc.Config()
	assert.Equal(t, "git@github.com:new/repo.git", got.RemoteURL,
		"Config() reflects the swapped value")
}

// TestService_ConfigReturnsDefensiveCopy pins that mutating the
// result of Config() does not bleed into Service state. Without
// the copy, a poorly-written caller (say, an E2E test or a
// future helper) could quietly rewrite the live config by
// mutating the value it pulled — and we'd lose the
// PUT-then-push guarantee that the operator expected.
func TestService_ConfigReturnsDefensiveCopy(t *testing.T) {
	db, dbPath := setupDB(t)
	svc := backup.New(backup.Config{
		MirrorDir: t.TempDir(),
		DBPath:    dbPath,
		RemoteURL: "git@github.com:a/repo.git",
	}, db)

	snap := svc.Config()
	snap.RemoteURL = "git@github.com:EVIL/repo.git"

	again := svc.Config()
	assert.Equal(t, "git@github.com:a/repo.git", again.RemoteURL,
		"Config() returns a defensive copy — caller mutation doesn't leak")
}

// TestService_UpdateConfig_ConcurrentReadersSeeNoTear is the canary
// for the atomic.Pointer change. Run with `go test -race ./internal/backup/...`
// — without atomic.Pointer, the reader goroutines would hit a
// data race on the cfg struct field.
func TestService_UpdateConfig_ConcurrentReadersSeeNoTear(t *testing.T) {
	db, dbPath := setupDB(t)
	svc := backup.New(backup.Config{
		MirrorDir: t.TempDir(),
		DBPath:    dbPath,
		RemoteURL: "git@github.com:initial/repo.git",
	}, db)

	// Mixed read/write workload. Readers do 100 reads per
	// iteration, writers do 200 swaps; -race catches any
	// unsynchronised access. Use a WaitGroup rather than a
	// sleeping + channel-close pattern — the test was leaking
	// goroutines that held t.TempDir() open past the test's
	// cleanup, producing spurious "directory not empty" failures.
	var wg sync.WaitGroup
	urlA := "git@github.com:initial/repo.git"
	urlB := "git@github.com:swapped/repo.git"

	for i := 0; i < 25; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				got := svc.Config().RemoteURL
				if got != urlA && got != urlB {
					t.Errorf("unexpected URL %q", got)
					return
				}
			}
		}()
	}
	for i := 0; i < 25; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				url := urlA
				if j%2 == 0 {
					url = urlB
				}
				svc.UpdateConfig(backup.Config{
					MirrorDir: t.TempDir(),
					DBPath:    dbPath,
					RemoteURL: url,
				})
			}
		}()
	}

	wg.Wait()
}
