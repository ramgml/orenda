// Package backup — scheduler that runs the periodic backup jobs.
//
// Phase 7.5 ships three tickers; Phase 32.7 replaces the fixed
// 24h snapshot ticker with a cron-driven fire loop that the
// operator can edit from /settings/backups. The current picture:
//
//   - GitPushInterval (default 5m) — CommitAndPush(mirror).
//     Simple ticker; unchanged from Phase 7.5.
//   - SnapshotCron    (default "0 3 * * *" = daily 03:00 UTC,
//     see DefaultSchedule) — Snapshot(). Cron-driven: the
//     scheduler reads cfg.SnapshotCron each iteration, computes
//     the next fire in UTC, and sleeps until then. PUT
//     /api/v1/backups/settings hot-reloads the expression via
//     Service.UpdateConfig (Phase 28.9 atomic.Pointer swap);
//     the next loop iteration picks up the new schedule.
//   - WALArchiveInterval (default 15m) — best-effort PRAGMA
//     wal_checkpoint(TRUNCATE). Unchanged from Phase 7.5.
//
// All jobs log to backup_log via RecordLog.
package backup

import (
	"context"
	"fmt"
	"sync"
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
	svc *Service
	// Push and WAL keep the fixed-interval ticker pattern from
	// Phase 7.5. Snapshot moved to a cron-driven loop below.
	pushInterval time.Duration
	walInterval  time.Duration
	// Phase Wave 4 PR 2: optional notifier for failure events.
	Notifier FailureNotifier
}

// NewScheduler returns a Scheduler with default intervals.
//
// push = 5m, wal = 15m (per PLAN#7.5). The snapshot fire is now
// cron-driven, so no snap interval is needed here — see
// DefaultSchedule for the initial cron expression.
func NewScheduler(svc *Service) *Scheduler {
	return &Scheduler{
		svc:          svc,
		pushInterval: 5 * time.Minute,
		walInterval:  15 * time.Minute,
	}
}

// WithIntervals overrides the push and WAL tick intervals. The
// snapshot fire is cron-driven (Phase 32.7) — change the schedule
// via UpdateConfig(SnapshotCron=...) instead. Useful in tests
// that need to compress the push/wal cadence.
func (s *Scheduler) WithIntervals(push, wal time.Duration) *Scheduler {
	if push > 0 {
		s.pushInterval = push
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

// Run blocks until ctx is cancelled. The push and WAL tickers
// run on this goroutine; the snapshot loop runs on its own
// goroutine so the cron-driven sleep (which can be days for a
// weekly schedule) doesn't block the push ticks.
func (s *Scheduler) Run(ctx context.Context) {
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		s.snapshotLoop(ctx)
	}()

	pushT := time.NewTicker(s.pushInterval)
	walT := time.NewTicker(s.walInterval)
	defer pushT.Stop()
	defer walT.Stop()

	for {
		select {
		case <-ctx.Done():
			wg.Wait()
			return
		case <-pushT.C:
			s.runPush(ctx)
		case <-walT.C:
			s.runCheckpoint(ctx)
		}
	}
}

// snapshotLoop is the cron-driven snapshot fire. It reads
// cfg.SnapshotCron fresh each iteration so a hot-reload via
// UpdateConfig takes effect on the next loop turn — the in-flight
// timer is allowed to the planned fire time, then re-read happens
// naturally. The fallback handles two failure modes:
//
//  1. cfg.SnapshotCron is empty — operators who never set it
//     still get daily snapshots at 03:00 UTC instead of an
//     unscheduled service.
//  2. cfg.SnapshotCron fails to parse — this shouldn't happen
//     during normal operation (config.Validate at startup +
//     the PUT handler both reject bad expressions), but a manual
//     SQL edit or future migration could land a bad value. The
//     fallback keeps the system scheduled and the notifier fans
//     out a `backup.failed` event so the operator sees the
//     drift in their tray.
func (s *Scheduler) snapshotLoop(ctx context.Context) {
	fallback := MustParse(DefaultSchedule)
	for {
		cfg := s.svc.Config()
		expr := cfg.SnapshotCron
		if expr == "" {
			expr = DefaultSchedule
		}
		sched, err := Parse(expr)
		if err != nil {
			s.notifyFailure(ctx, "snapshot_schedule", err)
			sched = fallback
		}
		next := sched.Next(time.Now().UTC())
		if next.IsZero() {
			// Structurally unsatisfiable schedule. Log, fall
			// back to the default, and let the next iteration
			// try again — the operator may have a temporary
			// fix in flight (e.g. about to PUT a corrected
			// expression).
			s.notifyFailure(ctx, "snapshot_schedule", fmt.Errorf("no fire within 5 years for %q", expr))
			sched = fallback
			next = sched.Next(time.Now().UTC())
			if next.IsZero() {
				// Even the fallback failed — the only way
				// that happens is "the package's default
				// expression rotted", which MustParse
				// already guarantees can't happen. Sleep
				// an hour and re-read cfg.
				timer := time.NewTimer(time.Hour)
				select {
				case <-ctx.Done():
					timer.Stop()
					return
				case <-timer.C:
					continue
				}
			}
		}

		timer := time.NewTimer(time.Until(next))
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
			s.runSnapshot(ctx)
		}
	}
}

// notifyFailure is a tiny helper that fans out a failure event
// when the scheduler can't continue with the configured state.
// It's the same seam runPush / runSnapshot / runCheckpoint use,
// but factored out so snapshotLoop can call it from the cron
// fallback path without reaching into the run* helpers (which
// expect a real Service method).
func (s *Scheduler) notifyFailure(ctx context.Context, op string, err error) {
	if s.Notifier != nil {
		s.Notifier.NotifyBackupFailed(ctx, op, err)
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

// runCheckpoint truncates the SQLite WAL via
// `PRAGMA wal_checkpoint(TRUNCATE)` — this bounds the WAL file
// size and checkpoints committed frames back into the main DB
// file. It is NOT a true WAL archive: we never copy WAL frames
// off-host for PITR. The audit (Phase 32.3) flagged the old
// `runWAL` / `wal_archive` naming as "dead code" because the
// name implied off-host shipping; the code itself was always
// wired into the scheduler and useful for WAL size management.
// Phase 32.8 renamed the function and the log type to make the
// contract explicit. See wiki:decision-log "WAL archive vs
// WAL checkpoint" for the decision and the rationale.
//
// A failure here doesn't surface to the user directly but lands
// in backup_log and (when wired) the FailureNotifier seam.
func (s *Scheduler) runCheckpoint(ctx context.Context) {
	_, err := s.svc.db.ExecContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`)
	status := "success"
	msg := ""
	if err != nil {
		status = "failed"
		msg = err.Error()
		if s.Notifier != nil {
			s.Notifier.NotifyBackupFailed(ctx, "wal_checkpoint", err)
		}
	}
	_ = s.svc.RecordLog(ctx, "wal_checkpoint", status, msg, "")
}
