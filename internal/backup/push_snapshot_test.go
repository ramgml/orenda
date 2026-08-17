package backup_test

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ramgml/orenda/internal/backup"
	"github.com/ramgml/orenda/internal/storage/sqlite"
)

// mustGit runs a git command in dir and fails the test on error.
func mustGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "git %s: %s", strings.Join(args, " "), string(out))
}

// initMirrorRepo creates a fresh git repo at dir with one initial
// commit so CommitAndPush has a HEAD to push.
func initMirrorRepo(t *testing.T, dir string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(dir, 0o755))
	mustGit(t, dir, "init", "-q", "--initial-branch=main")
	mustGit(t, dir, "config", "user.email", "test@orenda.local")
	mustGit(t, dir, "config", "user.name", "test")
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".gitkeep"), []byte{}, 0o644))
	mustGit(t, dir, "add", ".gitkeep")
	mustGit(t, dir, "commit", "-q", "-m", "init")
}

// TestPushWithSnapshot_StagesSnapshotAndManifest verifies that
// PushWithSnapshot stages the snapshot file and manifest in the
// mirror repo before attempting to push. No-remote config means
// CommitAndPush will fail at the final "git push" line; the staging
// artifacts must already be in place by that point.
func TestPushWithSnapshot_StagesSnapshotAndManifest(t *testing.T) {
	root := t.TempDir()
	db, dbPath := setupDB(t)
	defer db.Close()
	mirrorDir := filepath.Join(root, "mirror")
	snapDir := filepath.Join(root, "snapshots")
	initMirrorRepo(t, mirrorDir)

	svc := backup.New(backup.Config{
		MirrorDir:   mirrorDir,
		SnapshotDir: snapDir,
		DBPath:      dbPath,
		// RemoteURL empty → CommitAndPush fails at "git push". We
		// accept ErrPushFailed; the staging artifacts are what we
		// actually want to verify.
	}, db)

	err := svc.PushWithSnapshot(context.Background())
	require.Error(t, err)
	// Empty RemoteURL → CommitAndPush returns ErrNoRemote before
	// reaching the git-push step. Both ErrNoRemote and ErrPushFailed
	// are acceptable end-states; the assertion is that staging
	// artifacts are present regardless.
	assert.True(t,
		assert.ErrorIs(t, err, backup.ErrNoRemote) ||
			assert.ErrorIs(t, err, backup.ErrPushFailed),
		"expected ErrNoRemote or ErrPushFailed, got %v", err)

	// Snapshot file is staged.
	snapPath := filepath.Join(mirrorDir, "snapshots", "orenda-LATEST.db")
	stat, err := os.Stat(snapPath)
	require.NoError(t, err)
	assert.Greater(t, stat.Size(), int64(0))

	// Manifest exists and parses.
	manifestBytes, err := os.ReadFile(filepath.Join(mirrorDir, "snapshots", "manifest.json"))
	require.NoError(t, err)
	var manifest backup.SnapshotManifest
	require.NoError(t, json.Unmarshal(manifestBytes, &manifest))
	assert.Equal(t, "snapshots/orenda-LATEST.db", manifest.Latest)
	assert.NotEmpty(t, manifest.LatestSHA256)
	assert.Len(t, manifest.LatestSHA256, 64) // hex sha256
	assert.Equal(t, stat.Size(), manifest.LatestSize)
	assert.NotEmpty(t, manifest.Timestamp)
	// Schema version is read from migrations table; setupDB ran
	// all migrations so it should be > 0.
	assert.Greater(t, manifest.SchemaVersion, 0)
}

// TestLatestSnapshotFromMirror_NotFound returns ErrNotFound when
// the manifest hasn't been written yet.
func TestLatestSnapshotFromMirror_NotFound(t *testing.T) {
	root := t.TempDir()
	db, dbPath := setupDB(t)
	defer db.Close()
	mirrorDir := filepath.Join(root, "mirror")
	require.NoError(t, os.MkdirAll(mirrorDir, 0o755))

	svc := backup.New(backup.Config{
		MirrorDir:   mirrorDir,
		SnapshotDir: filepath.Join(root, "snapshots"),
		DBPath:      dbPath,
	}, db)

	_, _, err := svc.LatestSnapshotFromMirror()
	assert.ErrorIs(t, err, backup.ErrNotFound)
}

// TestLatestSnapshotFromMirror_HappyPath verifies the read-side
// helper returns the staged snapshot path + manifest and that the
// snapshot is recoverable (open + integrity_check passes).
func TestLatestSnapshotFromMirror_HappyPath(t *testing.T) {
	root := t.TempDir()
	db, dbPath := setupDB(t)
	defer db.Close()
	mirrorDir := filepath.Join(root, "mirror")
	snapDir := filepath.Join(root, "snapshots")
	initMirrorRepo(t, mirrorDir)

	svc := backup.New(backup.Config{
		MirrorDir:   mirrorDir,
		SnapshotDir: snapDir,
		DBPath:      dbPath,
	}, db)

	// Stage via the public path (no remote → push fails, but staging
	// succeeds).
	err := svc.PushWithSnapshot(context.Background())
	require.Error(t, err) // ErrPushFailed expected

	gotPath, gotManifest, err := svc.LatestSnapshotFromMirror()
	require.NoError(t, err)
	expectedPath := filepath.Join(mirrorDir, "snapshots", "orenda-LATEST.db")
	assert.Equal(t, expectedPath, gotPath)
	assert.NotEmpty(t, gotManifest.LatestSHA256)

	// Verify the staged snapshot is a valid sqlite DB by opening it
	// via the project's sqlite package — proves the snapshot is
	// usable as a recovery source (the actual goal of the gap).
	verifyDB, err := sqlite.Open(context.Background(), gotPath, sqlite.OpenConfig{
		WALMode: true, EnableForeign: true, BusyTimeoutMs: 5000,
	})
	require.NoError(t, err)
	defer verifyDB.Close()
	require.NoError(t, sqlite.Migrate(context.Background(), verifyDB, sqlite.MigrationsFS, "migrations"))
	// PRAGMA integrity_check returns "ok" for a valid sqlite file.
	var integrity string
	require.NoError(t, verifyDB.QueryRowContext(context.Background(), "PRAGMA integrity_check").Scan(&integrity))
	assert.Equal(t, "ok", integrity)
}
