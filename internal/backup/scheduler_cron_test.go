package backup_test

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ramgml/orenda/internal/backup"
)

// TestScheduler_SnapshotLoop_FiresOnCron pins that the cron-driven
// snapshot fire actually runs and records a sqlite_snapshot entry
// (Task 149). The loop's sleep-until-next-fire is injected
// (WithFireTimer), so the test drives the fire deterministically
// instead of waiting out a real "every minute" tick — the old
// real-tick version cost 44s of the suite budget.
//
// The deterministic path asserts the *logic*: schedule parse →
// Next() → fire → Snapshot() → RecordLog. The fact that the
// production timer truly wakes up is covered by the real-tick
// smoke (TestScheduler_RealTickSmoke, gated behind
// ORENDA_TEST_SLOW=1 and run via `make test-slow`) — do not
// delete one for the other.
func TestScheduler_SnapshotLoop_FiresOnCron(t *testing.T) {
	db, dbPath := setupDB(t)
	dir := t.TempDir()
	svc := backup.New(backup.Config{
		MirrorDir:    dir,
		SnapshotDir:  filepath.Join(dir, "snap"),
		DBPath:       dbPath,
		SnapshotCron: "* * * * *", // every minute
	}, db)

	ft := newManualFireTimer()
	sched := backup.NewScheduler(svc).
		WithIntervals(time.Hour, time.Hour).
		WithFireTimer(ft)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	done := make(chan struct{})
	go func() {
		sched.Run(ctx)
		close(done)
	}()

	// The loop arms the fire timer exactly once per iteration; the
	// first arm means it is parked waiting for the cron tick. Each
	// release fires one snapshot. Drive: wait for the arm, fire,
	// then poll for the sqlite_snapshot row — all deterministic,
	// no timed polling races (Task 149).
	require.Eventually(t, func() bool { return ft.armedCount() > 0 },
		2*time.Second, time.Millisecond, "loop should arm the fire timer")
	ft.release()

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
	}, 5*time.Second, 5*time.Millisecond, "injected fire should produce a snapshot log entry")

	cancel()
	<-done
}

// manualFireTimer is the test FireTimer (Task 149). Arm() counts
// the arm request and never fires on its own; release() unblocks
// the pending arm with an immediate fire. Concurrent access comes
// from the scheduler goroutine (Arm/Stop) and the test (release)
// — a mutex keeps -race happy.
type manualFireTimer struct {
	mu      sync.Mutex
	armed   int
	ch      chan time.Time
	stopped bool
}

func newManualFireTimer() *manualFireTimer { return &manualFireTimer{ch: make(chan time.Time)} }

func (m *manualFireTimer) Arm(_ context.Context, _ time.Duration) <-chan time.Time {
	m.mu.Lock()
	m.armed++
	m.mu.Unlock()
	return m.ch
}

func (m *manualFireTimer) Stop() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.stopped = true
}

// release fires the pending timer (no-op if none is armed).
func (m *manualFireTimer) release() {
	m.mu.Lock()
	armed := m.armed > 0
	m.mu.Unlock()
	if !armed {
		return
	}
	select {
	case m.ch <- time.Now():
	default:
	}
}

// armedCount reports how many times the loop requested a fire.
func (m *manualFireTimer) armedCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.armed
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

// TestScheduler_RealTickSmoke is the compensator for the injected
// FiresOnCron test (Task 149): it proves the PRODUCTION fire timer
// (time.NewTimer inside realFireTimer) actually wakes up and fires
// a snapshot on a real schedule tick — the class of failure the old
// sleep-based test covered ("scheduler wakes up in production").
//
// It waits out one real cron minute tick, so it runs only when
// ORENDA_TEST_SLOW=1 — never in the `make test` PR gate. Run it via
// `make test-slow` (nightly / pre-release) or directly:
//
//	ORENDA_TEST_SLOW=1 go test ./internal/backup/ -run TestScheduler_RealTickSmoke
func TestScheduler_RealTickSmoke(t *testing.T) {
	if os.Getenv("ORENDA_TEST_SLOW") != "1" {
		t.Skip("real-tick smoke: set ORENDA_TEST_SLOW=1 (or run `make test-slow`)")
	}
	db, dbPath := setupDB(t)
	dir := t.TempDir()
	svc := backup.New(backup.Config{
		MirrorDir:    dir,
		SnapshotDir:  filepath.Join(dir, "snap"),
		DBPath:       dbPath,
		SnapshotCron: "* * * * *", // fire within the next real minute
	}, db)

	// NO fire injection: the default realFireTimer path is under test.
	sched := backup.NewScheduler(svc).WithIntervals(time.Hour, time.Hour)

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	done := make(chan struct{})
	go func() {
		sched.Run(ctx)
		close(done)
	}()

	// 90s budget = 60s worst-case wait for the next minute boundary
	// + 30s slack for the VACUUM INTO + log write.
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
	}, 90*time.Second, time.Second, "real-tick scheduler should fire a snapshot within 90s")

	cancel()
	<-done
}
