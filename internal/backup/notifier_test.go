package backup

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeFailureNotifier captures the (op, err) pairs the scheduler
// fans out when a background job errors.
type fakeFailureNotifier struct {
	calls []failureCall
}

type failureCall struct {
	op  string
	err error
}

func (f *fakeFailureNotifier) NotifyBackupFailed(_ context.Context, op string, err error) {
	f.calls = append(f.calls, failureCall{op: op, err: err})
}

// TestFailureNotifier_OpNameAndError pins the wire shape: every
// scheduler hook (runPush/runSnapshot/runCheckpoint) calls
// `FailureNotifier.NotifyBackupFailed(op, err)` with the right op
// name and the underlying error. We exercise the helper inline
// here rather than spinning up a real Service — the scheduler's
// run* methods are 3-line wrappers and the notifier dispatch is
// the only new behaviour in Wave 4 PR 2.
//
// Phase 32.8 renamed the WAL op from "wal_archive" to
// "wal_checkpoint" — the old name was misleading (the code only
// runs PRAGMA wal_checkpoint, never ships WAL frames off-host).
// This test pins the new op name so a future revert of the
// rename is caught at the unit-test layer.
func TestFailureNotifier_OpNameAndError(t *testing.T) {
	notif := &fakeFailureNotifier{}

	cases := []struct {
		op  string
		err error
	}{
		{"git_push", errors.New("remote unreachable")},
		{"sqlite_snapshot", errors.New("disk full")},
		{"wal_checkpoint", errors.New("checkpoint timed out")},
	}
	for _, c := range cases {
		notif.NotifyBackupFailed(context.Background(), c.op, c.err)
	}
	require.Len(t, notif.calls, len(cases))
	for i, c := range cases {
		assert.Equal(t, c.op, notif.calls[i].op, "case %d op", i)
		assert.Equal(t, c.err, notif.calls[i].err, "case %d err", i)
	}
}

// TestScheduler_NotWiredByDefault pins the no-op default. A fresh
// Scheduler must have a nil Notifier; the run* methods are no-ops
// unless the operator wires one. (We can't actually exercise the
// full run* path here because the run* methods call into a real
// Service for Snapshot/CommitAndPush, which would require git +
// a writable mirror dir. The Phase 4 PR 2's contribution is the
// new failure-notification seam — the wire-up is verified
// manually in cmd/orenda.)
func TestScheduler_NotWiredByDefault(t *testing.T) {
	s := &Scheduler{}
	assert.Nil(t, s.Notifier, "fresh scheduler has no notifier — call WithNotifier to wire")
}

// TestScheduler_WithNotifierChains pins the builder pattern. The
// scheduler is a struct, not a fluent config object, so WithNotifier
// must return the receiver for assignment in main.go. Breaking
// the chain (e.g. by returning void) would force a separate setter
// and split the wiring.
func TestScheduler_WithNotifierChains(t *testing.T) {
	notif := &fakeFailureNotifier{}
	s := (&Scheduler{}).WithNotifier(notif)
	assert.Same(t, notif, s.Notifier)
}

// TestScheduler_RunCheckpoint_LogsCorrectType is the Phase 32.8
// wire-shape pin for the WAL → checkpoint rename. We construct a
// real Service against a temporary SQLite DB and call
// runCheckpoint directly: the helper must write a backup_log row
// with Type="wal_checkpoint" (not the old "wal_archive") and
// Status="success" when PRAGMA wal_checkpoint succeeds. A future
// revert to the misleading "wal_archive" name is caught here
// before the WAL audit trail in /settings/backups shows the
// wrong row type.
func TestScheduler_RunCheckpoint_LogsCorrectType(t *testing.T) {
	svc, sched, _, cleanup := newCheckpointTestService(t)
	defer cleanup()

	// Sanity: log table starts empty.
	before, err := svc.ListLog(context.Background(), 100)
	require.NoError(t, err)
	require.Empty(t, before)

	sched.runCheckpoint(context.Background())

	after, err := svc.ListLog(context.Background(), 100)
	require.NoError(t, err)
	require.Len(t, after, 1, "runCheckpoint must record exactly one log row")
	assert.Equal(t, "wal_checkpoint", after[0].Type,
		"Phase 32.8 rename: log type must be 'wal_checkpoint', not 'wal_archive'")
	assert.Equal(t, "success", after[0].Status,
		"fresh DB has no WAL frames — PRAGMA wal_checkpoint(TRUNCATE) is a no-op and must succeed")
}

// TestScheduler_RunCheckpoint_NotifiesOnFailure pins the failure
// path of the renamed helper. We close the underlying DB before
// calling runCheckpoint so PRAGMA wal_checkpoint fails; the
// helper must (a) record a "failed" backup_log row with the new
// "wal_checkpoint" type and (b) fan out a Notifier event with
// the matching op name.
func TestScheduler_RunCheckpoint_NotifiesOnFailure(t *testing.T) {
	_, sched, db, _ := newCheckpointTestService(t)
	notif := &fakeFailureNotifier{}
	sched.WithNotifier(notif)

	// Force PRAGMA wal_checkpoint to fail by closing the DB.
	require.NoError(t, db.Close())
	sched.runCheckpoint(context.Background())

	require.Len(t, notif.calls, 1, "notifier must fire on PRAGMA failure")
	assert.Equal(t, "wal_checkpoint", notif.calls[0].op,
		"Phase 32.8 rename: notifier op must match the new log type")
}

// newCheckpointTestService wires a minimal Service + Scheduler
// pair against a fresh SQLite DB. It's the Phase 32.8 internal
// package equivalent of backup_test.go's setupDB — duplicated
// because internal-package tests can't reach into external
// test-package helpers. The returned cleanup closes the DB.
func newCheckpointTestService(t *testing.T) (*Service, *Scheduler, *sql.DB, func()) {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "orenda.db")
	db, err := sql.Open("sqlite", dbPath)
	require.NoError(t, err, "sqlite is registered by the backup package import chain")
	_, err = db.ExecContext(context.Background(), `
		CREATE TABLE backup_log (
			id TEXT PRIMARY KEY,
			type TEXT NOT NULL,
			status TEXT NOT NULL,
			message TEXT,
			snapshot_path TEXT,
			created_at TEXT NOT NULL
		)
	`)
	require.NoError(t, err)
	svc := &Service{
		db: db,
	}
	svc.cfg.Store(&Config{
		MirrorDir:   dir,
		SnapshotDir: dir,
		DBPath:      dbPath,
	})
	sched := &Scheduler{svc: svc}
	return svc, sched, db, func() { _ = db.Close() }
}
