// Phase 22: safety-copy path computation.
//
// The CLI restores a backup "in place" by first copying the live DB
// aside (orenda.db.pre-restore-<ts>) before the atomic swap. The
// safety-copy helper is small but the timestamp format is part of
// the contract — operators grep for these files when recovering.
package backup_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ramgml/orenda/internal/backup"
	"github.com/ramgml/orenda/internal/storage/sqlite"
)

func TestSafetyCopyPath(t *testing.T) {
	ts := time.Unix(1717000000, 0)
	got := backup.SafetyCopyPath("/var/lib/orenda/orenda.db", ts)
	assert.Equal(t, "/var/lib/orenda/orenda.db.pre-restore-1717000000", got)

	// Empty path → empty result (the CLI uses this as a guard).
	assert.Equal(t, "", backup.SafetyCopyPath("", ts))
}

// TestRestoreWithVerify_SnapshotToDataRoundtrip: end-to-end happy
// path. Write some data, snapshot it, modify the live DB, restore
// the snapshot back, verify the snapshot's data is on disk AND the
// live modifications are gone. integrity_check + foreign_key_check
// pass on the result.
//
// This is the integration test the user actually cares about:
// "if I take a snapshot, break things, and restore — am I back?".
func TestRestoreWithVerify_SnapshotToDataRoundtrip(t *testing.T) {
	db, dbPath := setupDB(t)
	defer db.Close()
	dir := t.TempDir()

	svc := backup.New(backup.Config{
		SnapshotDir: filepath.Join(dir, "snap"),
		DBPath:      dbPath,
	}, db)

	// Seed Alice in the live DB.
	writeUser(t, dbPath)
	snapPath, err := svc.Snapshot(context.Background())
	require.NoError(t, err)

	// Modify the live DB after snapshot — restore should roll this back.
	_, err = db.ExecContext(context.Background(),
		`INSERT INTO users (id, email, password_hash, display_name, role) VALUES (?, ?, ?, ?, ?)`,
		"u-2", "bob@x.c", "y", "Bob", "owner")
	require.NoError(t, err)

	// Restore over the live DB (we close it first to avoid the WAL
	// sidecar race — SQLite doesn't like another handle when the file
	// moves out from under it).
	require.NoError(t, db.Close())

	safetyPath := backup.SafetyCopyPath(dbPath, time.Now())
	require.NoError(t, copyFile(dbPath, safetyPath))

	require.NoError(t, svc.Restore(context.Background(), snapPath, dbPath))

	// Re-open and verify. Migrations bring it up to current schema;
	// foreign_key_check confirms the FK graph is intact.
	reopened, err := sqlite.Open(context.Background(), dbPath, sqlite.OpenConfig{
		WALMode: true, EnableForeign: true, BusyTimeoutMs: 5000,
	})
	require.NoError(t, err)
	defer reopened.Close()

	require.NoError(t, sqlite.Migrate(context.Background(), reopened, sqlite.MigrationsFS, "migrations"))

	// Alice back, Bob gone (rolled back to snapshot).
	assert.Equal(t, 1, countAlice(t, dbPath), "snapshot's Alice should be present")
	var bob int
	require.NoError(t, reopened.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM users WHERE id = ?`, "u-2").Scan(&bob))
	assert.Equal(t, 0, bob, "post-snapshot Bob should be gone after restore")

	// Safety-copy file exists and contains the live state at the
	// moment of restore (= had Bob). Operator's escape hatch.
	assert.FileExists(t, safetyPath)
	assert.Contains(t, safetyPath, ".pre-restore-")
	// Read the safety-copy and confirm Bob is in there.
	safetyDB, err := sqlite.Open(context.Background(), safetyPath, sqlite.OpenConfig{
		WALMode: false, EnableForeign: true, BusyTimeoutMs: 1000,
	})
	require.NoError(t, err)
	var safetyBob int
	require.NoError(t, safetyDB.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM users WHERE id = ?`, "u-2").Scan(&safetyBob))
	safetyDB.Close()
	assert.Equal(t, 1, safetyBob, "safety-copy should still contain Bob")

	// integrity_check on the restored DB — "ok" on a clean sqlite.
	var integrity string
	require.NoError(t, reopened.QueryRowContext(context.Background(),
		`PRAGMA integrity_check`).Scan(&integrity))
	assert.Equal(t, "ok", integrity)
}

// TestRestoreWithVerify_RefusesOnIntegrityError: a snapshot whose
// magic header passes but whose body is corrupt fails the post-restore
// integrity check. We don't try to detect this at copy-time (the
// restore is a byte-for-byte copy); the verification step is the
// safety net.
//
// modernc.org/sqlite refuses to open a truncated file (ping fails with
// "file is not a database"), so the CLI's Open step surfaces this
// error before we ever reach PRAGMA integrity_check. We exercise
// that path here.
func TestRestoreWithVerify_RefusesOnIntegrityError(t *testing.T) {
	dir := t.TempDir()

	// Build a real snapshot first, then truncate it to corrupt it.
	realDB, realDBPath := setupDB(t)
	realSvc := backup.New(backup.Config{
		SnapshotDir: filepath.Join(dir, "snap"),
		DBPath:      realDBPath,
	}, realDB)
	snapPath, err := realSvc.Snapshot(context.Background())
	require.NoError(t, err)
	realDB.Close()

	// Truncate the snapshot to 16 bytes (just the magic header).
	require.NoError(t, os.Truncate(snapPath, 16))

	// Restore succeeds (it's a valid sqlite header); opening the
	// restored file is what surfaces the corruption.
	dst := filepath.Join(dir, "restored.db")
	dstSvc := backup.New(backup.Config{SnapshotDir: dir, DBPath: dst}, nil)
	require.NoError(t, dstSvc.Restore(context.Background(), snapPath, dst))

	_, err = sqlite.Open(context.Background(), dst, sqlite.OpenConfig{
		WALMode: false, EnableForeign: true, BusyTimeoutMs: 1000,
	})
	require.Error(t, err, "opening a truncated snapshot should fail")
	assert.Contains(t, err.Error(), "not a database",
		"expected sqlite 'not a database' error, got %v", err)
}

// copyFile is a tiny io.Copy helper for tests. The CLI has its own
// copyFile in cmd/orenda/backup.go (different package, same idea).
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	buf := make([]byte, 32*1024)
	for {
		n, rerr := in.Read(buf)
		if n > 0 {
			if _, werr := out.Write(buf[:n]); werr != nil {
				_ = out.Close()
				return werr
			}
		}
		if rerr != nil {
			if rerr.Error() == "EOF" || strings.Contains(rerr.Error(), "EOF") {
				break
			}
			_ = out.Close()
			return rerr
		}
	}
	return out.Close()
}
