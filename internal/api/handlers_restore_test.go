// T374: the HTTP restore handler (Settings → Backups → Restore) must
// obey the same verify-before-swap order as the CLI: the snapshot
// lands in a staging copy next to the live database, verify runs on
// the copy, and only a verified copy is swapped in. A failed verify
// leaves the live file byte-for-byte intact with no artifact left
// behind — on the UI's main recovery surface, not just the operator
// CLI. File-based fixtures are sqlite-only: the postgres leg of the
// handler never swaps files (T366 scratch-restore, covered in
// internal/backup).
package api_test

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/ramgml/orenda/internal/api"
	"github.com/ramgml/orenda/internal/auth"
	"github.com/ramgml/orenda/internal/backup"
	"github.com/ramgml/orenda/internal/domain/user"
	"github.com/ramgml/orenda/internal/storage/sqlite"
	"github.com/ramgml/orenda/internal/testutil/pgtest"
)

// restoreFixture wires the auth surface plus the restore handler's
// Backup service and live DBPath. The *sql.DB stays the fixture's own
// pool; the restore handler itself works on the FILE at DBPath.
type restoreFixture struct {
	router http.Handler
	cookie string
}

func newRestoreFixture(t *testing.T, db *sql.DB, livePath, snapDir string) *restoreFixture {
	t.Helper()
	users := sqlite.NewUserRepository(db)
	u := &user.User{
		Email:        "restore@x.com",
		PasswordHash: mustHashFast(t),
		DisplayName:  "R",
	}
	require.NoError(t, users.Create(context.Background(), u))

	signer := auth.NewSigner("test-secret-32-bytes-long-xxxxx", time.Hour, "orenda")
	deps := &api.Dependencies{
		Logger:     zap.NewNop(),
		Signer:     signer,
		Users:      users,
		CookieName: "orenda_session",
		Backup:     backup.New(backup.Config{SnapshotDir: snapDir, DBPath: livePath}, db),
		DBPath:     livePath,
	}
	router := api.NewRouter(deps)
	t.Cleanup(deps.RateLimitClose)

	cookie := loginAndCookie(t, router, "restore@x.com", "hunter2!")
	return &restoreFixture{router: router, cookie: cookie}
}

// post sends an authenticated JSON POST through the router.
func (f *restoreFixture) post(t *testing.T, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	return authedJSON(t, f.router, http.MethodPost, path, f.cookie, body)
}

// requireMaintenanceOn flips maintenance via the HTTP toggle (the same
// call the UI's Restore button makes) and asserts the flag.
func (f *restoreFixture) requireMaintenanceOn(t *testing.T) {
	t.Helper()
	rr := f.post(t, "/api/v1/maintenance/on", nil)
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	require.True(t, api.IsMaintenanceOn())
}

// seedOrphanBoardFile re-creates the legacy damage directly in the
// live file (dedicated connection, FK off — exactly like migration 015
// left upgraded installs): a board whose project row is gone.
func seedOrphanBoardFile(t *testing.T, ctx context.Context, dbPath string) {
	t.Helper()
	db, err := sqlite.Open(ctx, dbPath, sqlite.OpenConfig{
		WALMode: true, EnableForeign: true, BusyTimeoutMs: 5000,
	})
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	_, err = db.ExecContext(ctx, `PRAGMA foreign_keys = OFF`)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `
		INSERT INTO boards (id, project_id, name, position)
		VALUES ('70194de4-ac95-48ca-818c-000000000001',
		        '00000000-0000-0000-0000-00000000cafe', 'Main', 0)`)
	require.NoError(t, err)
	// Fold the insert into the main db file: the safety copy (like the
	// CLI's) mirrors the main file, and a quiesced install has no
	// uncheckpointed WAL frames at restore time.
	_, err = db.ExecContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`)
	require.NoError(t, err)
}

// countOrphanBoards opens path and counts the legacy orphan boards.
func countOrphanBoards(t *testing.T, ctx context.Context, dbPath string) int {
	t.Helper()
	db, err := sqlite.Open(ctx, dbPath, sqlite.OpenConfig{
		WALMode: true, EnableForeign: true, BusyTimeoutMs: 5000,
	})
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	var n int
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT count(*) FROM boards WHERE project_id = '00000000-0000-0000-0000-00000000cafe'`).Scan(&n))
	return n
}

// requireNoRestoreArtifacts asserts the T374 "FAIL → артефакт не
// оставлен" contract next to the live file. The -wal/-shm sidecars
// are NOT asserted here: the fixture's own pool legitimately keeps
// them open for the whole test — the full pattern set (including
// sidecars) is pinned by the CLI tests in cmd/orenda, where nothing
// holds the live file open.
func requireNoRestoreArtifacts(t *testing.T, livePath string) {
	t.Helper()
	for _, pattern := range []string{
		livePath + ".restore-staging-*",
		livePath + ".restore.tmp*",
		livePath + ".pre-restore-*",
	} {
		matches, err := filepath.Glob(pattern)
		require.NoError(t, err)
		assert.Empty(t, matches, "a failed restore must leave no %s artifact", pattern)
	}
}

// TestRestoreHandler_VerifyFailLeavesLiveIntact is the T374 DoD
// contract on the UI path: restoring a tainted snapshot under
// maintenance returns 422 verify_failed, the live database file stays
// byte-for-byte intact, and no artifact (staging copy, .restore.tmp,
// safety copy, sidecars) is left behind.
func TestRestoreHandler_VerifyFailLeavesLiveIntact(t *testing.T) {
	if pgtest.ActiveDriver() == pgtest.DriverPostgres {
		t.Skip("the restore handler's sqlite branch swaps FILES — runs on the sqlite leg (pg leg never swaps files, T366)")
	}
	t.Cleanup(func() { api.MaintenanceOff() })
	ctx := context.Background()

	db, dir := copyTemplateDB(t)
	livePath := filepath.Join(dir, "orenda.db")

	seedOrphanBoardFile(t, ctx, livePath)
	snapDir := filepath.Join(dir, "snapshots")
	svc := backup.New(backup.Config{SnapshotDir: snapDir, DBPath: livePath}, db)
	snap, err := svc.Snapshot(ctx)
	require.NoError(t, err)

	liveBefore, err := os.ReadFile(livePath)
	require.NoError(t, err)

	f := newRestoreFixture(t, db, livePath, snapDir)
	f.requireMaintenanceOn(t)

	rr := f.post(t, "/api/v1/backups/restore", map[string]any{"path": snap, "force": true})
	require.Equal(t, http.StatusUnprocessableEntity, rr.Code,
		"a tainted snapshot must fail restore verify; body: %s", rr.Body.String())
	assert.Contains(t, rr.Body.String(), "verify_failed")
	assert.Contains(t, rr.Body.String(), "foreign_key_check",
		"the 422 detail must carry the T369 violation list")

	liveAfter, err := os.ReadFile(livePath)
	require.NoError(t, err)
	assert.Equal(t, string(liveBefore), string(liveAfter),
		"a failed restore must leave the live database byte-for-byte intact")
	requireNoRestoreArtifacts(t, livePath)
}

// TestRestoreHandler_SuccessSwapsVerifiedStaging pins the success half
// of the T374 order on the UI path: the swap installs the VERIFIED
// staging copy (post-snapshot live damage vanishes), the swap-time
// safety copy holds the PRE-restore live data (rollback parity with
// the CLI), and no staging artifact survives the rename.
func TestRestoreHandler_SuccessSwapsVerifiedStaging(t *testing.T) {
	if pgtest.ActiveDriver() == pgtest.DriverPostgres {
		t.Skip("the restore handler's sqlite branch swaps FILES — runs on the sqlite leg (pg leg never swaps files, T366)")
	}
	t.Cleanup(func() { api.MaintenanceOff() })
	ctx := context.Background()

	db, dir := copyTemplateDB(t)
	livePath := filepath.Join(dir, "orenda.db")

	snapDir := filepath.Join(dir, "snapshots")
	svc := backup.New(backup.Config{SnapshotDir: snapDir, DBPath: livePath}, db)
	snap, err := svc.Snapshot(ctx)
	require.NoError(t, err)

	// Live damage created AFTER the snapshot must vanish on restore,
	// proving the swap installed the staged snapshot copy.
	seedOrphanBoardFile(t, ctx, livePath)

	f := newRestoreFixture(t, db, livePath, snapDir)
	f.requireMaintenanceOn(t)

	rr := f.post(t, "/api/v1/backups/restore", map[string]any{"path": snap, "force": true})
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	assert.Contains(t, rr.Body.String(), "restored")

	assert.Zero(t, countOrphanBoards(t, ctx, livePath),
		"the live database must carry the snapshot's data after restore")

	safety, err := filepath.Glob(livePath + ".pre-restore-*")
	require.NoError(t, err)
	require.Len(t, safety, 1, "a restore keeps exactly one safety copy of the live database")
	assert.Equal(t, 1, countOrphanBoards(t, ctx, safety[0]),
		"the safety copy must hold the pre-restore live data")

	matches, err := filepath.Glob(livePath + ".restore-staging-*")
	require.NoError(t, err)
	assert.Empty(t, matches, "a successful restore leaves no staging copy behind")
}
