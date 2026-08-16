// Package backup — scheduler that runs the periodic backup jobs.
//
// Phase 7.5 ships three tickers on a single goroutine:
//
//   - GitPushInterval   (default 5m)   → CommitAndPush(mirror)
//   - SnapshotCron      (default daily at 03:00, simplified to
//     SnapshotInterval 24h) → Snapshot()
//   - WALArchiveInterval (default 15m) — best-effort checkpoint; the
//     SQLite WAL is rotated via PRAGMA wal_checkpoint(TRUNCATE)
//
// All jobs log to backup_log via RecordLog.
package backup

import (
	"context"
	"fmt"
	"time"
)

// FailureNotifier is the slim seam the scheduler uses to emit
// `backup.failed` events when a background job errors. nil is OK
// (events are best-effort). Phase Wave 4 PR 2 closes the audit
// gap "Notifier не эмитит backup.failed".
type FailureNotifier interface {
	NotifyBackupFailed(ctx context.Context, op string, err error)
}

// Scheduler drives the periodic backup jobs.
type Scheduler struct {
	svc          *Service
	pushInterval time.Duration
	snapInterval time.Duration
	walInterval  time.Duration
	// Phase Wave 4 PR 2: optional notifier for failure events.
	Notifier FailureNotifier
}

// NewScheduler returns a Scheduler with default intervals.
//
// push = 5m, snap = 24h, wal = 15m (per PLAN#7.5).
func NewScheduler(svc *Service) *Scheduler {
	return &Scheduler{
		svc:          svc,
		pushInterval: 5 * time.Minute,
		snapInterval: 24 * time.Hour,
		walInterval:  15 * time.Minute,
	}
}

// WithIntervals overrides the tick intervals (useful in tests).
func (s *Scheduler) WithIntervals(push, snap, wal time.Duration) *Scheduler {
	if push > 0 {
		s.pushInterval = push
	}
	if snap > 0 {
		s.snapInterval = snap
	}
	if wal > 0 {
		s.walInterval = wal
	}
	return s
}

// WithNotifier wires a FailureNotifier. Phase Wave 4 PR 2:
// `backup.failed` events fire on every scheduled-job error when
// this is set; nil is fine (events are best-effort).
func (s *Scheduler) WithNotifier(n FailureNotifier) *Scheduler {
	s.Notifier = n
	return s
}

// Run blocks until ctx is cancelled.
func (s *Scheduler) Run(ctx context.Context) {
	pushT := time.NewTicker(s.pushInterval)
	snapT := time.NewTicker(s.snapInterval)
	walT := time.NewTicker(s.walInterval)
	defer pushT.Stop()
	defer snapT.Stop()
	defer walT.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-pushT.C:
			s.runPush(ctx)
		case <-snapT.C:
			s.runSnapshot(ctx)
		case <-walT.C:
			s.runWAL(ctx)
		}
	}
}

func (s *Scheduler) runPush(ctx context.Context) {
	err := s.svc.CommitAndPush(ctx, fmt.Sprintf("auto backup %s", time.Now().UTC().Format(time.RFC3339)))
	status := "success"
	msg := ""
	if err != nil {
		status = "failed"
		msg = err.Error()
		if s.Notifier != nil {
			s.Notifier.NotifyBackupFailed(ctx, "git_push", err)
		}
	}
	_ = s.svc.RecordLog(ctx, "git_push", status, msg, "")
}

func (s *Scheduler) runSnapshot(ctx context.Context) {
	path, err := s.svc.Snapshot(ctx)
	status := "success"
	msg := ""
	if err != nil {
		status = "failed"
		msg = err.Error()
		if s.Notifier != nil {
			s.Notifier.NotifyBackupFailed(ctx, "sqlite_snapshot", err)
		}
	}
	_ = s.svc.RecordLog(ctx, "sqlite_snapshot", status, msg, path)
}

func (s *Scheduler) runWAL(ctx context.Context) {
	// Best-effort WAL checkpoint; a failure doesn't surface to the user
	// directly but lands in the log for the operator.
	_, err := s.svc.db.ExecContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`)
	status := "success"
	msg := ""
	if err != nil {
		status = "failed"
		msg = err.Error()
		if s.Notifier != nil {
			s.Notifier.NotifyBackupFailed(ctx, "wal_archive", err)
		}
	}
	_ = s.svc.RecordLog(ctx, "wal_archive", status, msg, "")
}
