package backup_test

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ramgml/orenda/internal/backup"
)

// TestScheduler_SnapshotLoop_FiresOnCron is the integration pin for
// the Phase 32.7 cron-driven fire. We point the scheduler at a
// "fire every minute" schedule, wait through one cron tick, and
// verify the Service's RecordLog saw a sqlite_snapshot entry.
//
// The "every minute" schedule is the shortest sane value and the
// next fire is always within 60s. We use the WithIntervals override
// to compress the push/wal tickers to ~1h (the test only cares
// about the snapshot side-effect, not the push/wal cadence).
//
// The test asserts:
//
//  1. The scheduler runs without panicking on a valid expr.
//  2. At least one sqlite_snapshot log row is produced within a
//     90-second budget (60s for the next cron tick + 30s slack).
//
// Cancel-on-ctx.Done is implicit: if the test fails the loop
// goroutine is bounded by the test's defer cancel.
func TestScheduler_SnapshotLoop_FiresOnCron(t *testing.T) {
	db, dbPath := setupDB(t)
	dir := t.TempDir()
	svc := backup.New(backup.Config{
		MirrorDir:    dir,
		SnapshotDir:  filepath.Join(dir, "snap"),
		DBPath:       dbPath,
		SnapshotCron: "* * * * *", // every minute
	}, db)

	sched := backup.NewScheduler(svc).WithIntervals(time.Hour, time.Hour)

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	done := make(chan struct{})
	go func() {
		sched.Run(ctx)
		close(done)
	}()

	// Poll for the snapshot log. 90s budget = 60s for the first
	// cron tick + 30s slack for the VACUUM INTO + log write.
	require.Eventually(t, func() bool {
		entries, err := svc.ListLog(ctx, 10)
		if err != nil {
			return false
		}
		for _, e := range entries {
			if e.Type == "sqlite_snapshot" && e.Status == "success" {
				return true
			}
		}
		return false
	}, 90*time.Second, time.Second, "scheduler should fire a snapshot within 90s")

	cancel()
	<-done
}

// TestScheduler_SnapshotLoop_HonorsConfigSwap verifies the
// hot-reload contract: changing cfg.SnapshotCron via
// UpdateConfig while the loop is running takes effect on the
// next iteration. We assert the loop survives the swap and the
// Service's Config() reflects the new value; we can't observe
// the fire directly without sleeping until the new schedule, so
// the assertion is on the live config read.
//
// The run-loop boundary we test is the one documented in the
// Phase 32.7 snapshotLoop comment: "reads cfg.SnapshotCron
// fresh each iteration so a hot-reload via UpdateConfig takes
// effect on the next loop turn".
func TestScheduler_SnapshotLoop_HonorsConfigSwap(t *testing.T) {
	db, dbPath := setupDB(t)
	dir := t.TempDir()
	svc := backup.New(backup.Config{
		MirrorDir:    dir,
		SnapshotDir:  filepath.Join(dir, "snap"),
		DBPath:       dbPath,
		SnapshotCron: "0 3 * * *",
	}, db)

	sched := backup.NewScheduler(svc).WithIntervals(time.Hour, time.Hour)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		sched.Run(ctx)
		close(done)
	}()

	// Hot-reload: switch the schedule to every-minute.
	svc.UpdateConfig(backup.Config{
		MirrorDir:    dir,
		SnapshotDir:  filepath.Join(dir, "snap"),
		DBPath:       dbPath,
		SnapshotCron: "* * * * *",
	})

	require.Eventually(t, func() bool {
		return svc.Config().SnapshotCron == "* * * * *"
	}, time.Second, 10*time.Millisecond,
		"Config() should reflect the hot-swap")
}

// TestScheduler_SnapshotLoop_FallsBackOnInvalid is the safety
// net for a corrupted snapshot_cron (e.g. an operator pasting
// a bad string via a future migration or manual SQL). The loop
// must keep running on the hard-coded DefaultSchedule rather
// than silently disabling snapshots.
//
// We use the notifier seam to assert the operator is informed
// about the fallback via the `snapshot_schedule` op, even
// though the production notifier fans out to the event tray.
func TestScheduler_SnapshotLoop_FallsBackOnInvalid(t *testing.T) {
	db, dbPath := setupDB(t)
	dir := t.TempDir()
	svc := backup.New(backup.Config{
		MirrorDir:    dir,
		SnapshotDir:  filepath.Join(dir, "snap"),
		DBPath:       dbPath,
		SnapshotCron: "not a cron",
	}, db)

	notif := &capturingNotifier{}
	sched := backup.NewScheduler(svc).
		WithIntervals(time.Hour, time.Hour).
		WithNotifier(notif)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		sched.Run(ctx)
		close(done)
	}()

	require.Eventually(t, func() bool {
		for _, c := range notif.snapshotForTest() {
			if c.op == "snapshot_schedule" {
				return true
			}
		}
		return false
	}, 2*time.Second, 50*time.Millisecond,
		"loop must notify on invalid cron expr")

	assert.Equal(t, "not a cron", svc.Config().SnapshotCron,
		"Config() should still hold the bad expr; the loop's fallback is internal")
}

// TestScheduler_SnapshotLoop_StopsOnCtxCancel pins that the
// cron-driven sleep returns promptly on shutdown. A "fire next
// leap year" schedule is the worst case — the timer would
// otherwise block the snapshotLoop goroutine for hours, leaking
// past the test's cleanup. This regression-tests the
// snapshotLoop's select on ctx.Done.
func TestScheduler_SnapshotLoop_StopsOnCtxCancel(t *testing.T) {
	db, dbPath := setupDB(t)
	dir := t.TempDir()
	svc := backup.New(backup.Config{
		MirrorDir:    dir,
		SnapshotDir:  filepath.Join(dir, "snap"),
		DBPath:       dbPath,
		SnapshotCron: "0 0 29 2 *", // next fire is at most ~4 years out
	}, db)

	sched := backup.NewScheduler(svc).WithIntervals(time.Hour, time.Hour)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		sched.Run(ctx)
		close(done)
	}()
	cancel()

	select {
	case <-done:
		// success — Run returned within the cancel window
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return within 2s of cancel; " +
			"snapshotLoop is probably blocking on the cron timer")
	}
}

// capturingNotifier is the test-local FailureNotifier fake. It
// mirrors fakeFailureNotifier from notifier_test.go but lives
// here so the cron tests stay self-contained — the production
// notifier fans out to the event hub, which the scheduler tests
// don't need to exercise. The mutex guards concurrent access from
// the scheduler goroutine and the test's Eventually reader
// (caught by `go test -race`).
type capturingNotifier struct {
	mu    sync.Mutex
	calls []notifCall
}

type notifCall struct {
	op  string
	err error
}

func (n *capturingNotifier) NotifyBackupFailed(_ context.Context, op string, err error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.calls = append(n.calls, notifCall{op: op, err: err})
}

// snapshotForTest takes a consistent view of the calls list. The
// scheduler goroutine may be appending concurrently; locking here
// keeps the race detector happy even though the eventual assertion
// (HasCall) is the only thing that cares.
func (n *capturingNotifier) snapshotForTest() []notifCall {
	n.mu.Lock()
	defer n.mu.Unlock()
	out := make([]notifCall, len(n.calls))
	copy(out, n.calls)
	return out
}
