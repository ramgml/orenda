// Package task — Task 87: status-driven auto time tracking.
//
// The agent delegation loop previously produced no time records: the
// Phase 4.6 agent timer routes never landed, and nothing in the task
// lifecycle opened a time entry. This file makes time tracking a
// consequence of the task status itself — time flows while a task is
// in in_progress:
//
//	enter in_progress (claim / kanban move / PATCH / reject-reopen)
//	  → open a TimeEntry for the actor (stale-guard: an already-open
//	    interval is closed and accrued first, so the one-open-timer
//	    invariant survives and no time is lost);
//
//	leave in_progress (submit → review, approve → done, kanban move,
//	  release, PATCH) → close the actor's open entry on this task via
//	  CloseAndAccrue (ended_at + duration_s + tasks.time_spent_s).
//
// The transition logic lives in the task service (not in handlers) so
// every write path that flips status shares one implementation.
package task

import (
	"context"
	"time"

	"go.uber.org/zap"

	"github.com/ramgml/orenda/internal/domain/task"
	"github.com/ramgml/orenda/internal/domain/timeentry"
)

// TimeEntries is the narrow surface the auto-timer needs from the
// time-entry storage. *sqlite.TimeEntryRepo satisfies it directly;
// the richer timeentry.Service is deliberately not required — the
// transition policy (when to open, what the stale-guard does) belongs
// to the task lifecycle, while the storage layer already guarantees
// the single-active-timer invariant and atomic accrual.
type TimeEntries interface {
	Create(ctx context.Context, e *timeentry.TimeEntry) (*timeentry.TimeEntry, error)
	FindOpen(ctx context.Context, agentID string) (*timeentry.TimeEntry, error)
	CloseAndAccrue(ctx context.Context, e *timeentry.TimeEntry) error
}

// syncTimer opens or closes the actor's time entry to match a status
// transition of tr. prevStatus is the status before the caller mutated
// the task; no-op when the status did not actually change, when no
// TimeEntries backend is wired (partial fixtures), or when the actor
// cannot be resolved.
//
// Best-effort by design: a timer failure must never fail the task
// write that caused it — problems are logged and dropped.
func (s *Service) syncTimer(ctx context.Context, tr *task.Task, prevStatus task.Status) {
	if s == nil || s.Time == nil || tr == nil || tr.Status == prevStatus {
		return
	}
	actor := s.timerActor(ctx, tr)
	s.syncTimerAs(ctx, tr, prevStatus, actor)
}

// syncTimerAs is syncTimer with an explicit actor. Release needs it:
// the assignee is cleared before the timer is closed, so the actor
// must be captured by the caller before the mutation.
func (s *Service) syncTimerAs(ctx context.Context, tr *task.Task, prevStatus task.Status, actorID string) {
	if s == nil || s.Time == nil || tr == nil || tr.Status == prevStatus || actorID == "" {
		return
	}
	if tr.Status == task.StatusInProgress {
		s.startTimerOnTask(ctx, tr.ID, actorID)
		return
	}
	s.stopTimerOnTask(ctx, tr.ID, actorID)
}

// timerActor attributes auto-timer entries to the task assignee; for
// unassigned tasks (owner-driven kanban moves, PATCHes) it falls back
// to the project owner — the same convention the /today active-timer
// probe uses for single-owner installs.
func (s *Service) timerActor(ctx context.Context, tr *task.Task) string {
	if tr.AssigneeID != "" {
		return tr.AssigneeID
	}
	if s.Columns == nil || tr.ProjectID == "" {
		return ""
	}
	p, err := s.Columns.GetProject(ctx, tr.ProjectID)
	if err != nil || p == nil {
		return ""
	}
	return p.OwnerID
}

// startTimerOnTask opens a fresh entry for actorID on taskID. The
// stale-guard: when the actor already has an open interval (from a
// crashed session, a previous claim, …) it is closed and accrued
// first (end=now, duration lands on the old task), then the new entry
// opens — the one-open-timer invariant holds and no time is lost.
// If the open interval already belongs to this task it is left alone.
func (s *Service) startTimerOnTask(ctx context.Context, taskID, actorID string) {
	open, err := s.Time.FindOpen(ctx, actorID)
	if err != nil {
		s.logTimerWarn("startTimerOnTask: find open entry", actorID, taskID, err)
		return
	}
	if open != nil {
		if open.TaskID == taskID {
			// Already tracking this task — keep the running interval.
			return
		}
		if !closeOpenEntry(ctx, s.Time, open) {
			// Could not close the stale interval; creating a new one
			// would violate the invariant, so bail out this round.
			return
		}
	}
	e := &timeentry.TimeEntry{
		TaskID:    taskID,
		AgentID:   actorID,
		StartedAt: time.Now().UTC(),
		Source:    timeentry.SourceTimer,
	}
	if _, err := s.Time.Create(ctx, e); err != nil {
		s.logTimerWarn("startTimerOnTask: create entry", actorID, taskID, err)
	}
}

// stopTimerOnTask closes the actor's open entry, but only when it
// belongs to taskID — an open interval on a different task (the
// one-open-timer invariant means there is at most one) is not ours to
// stop.
func (s *Service) stopTimerOnTask(ctx context.Context, taskID, actorID string) {
	open, err := s.Time.FindOpen(ctx, actorID)
	if err != nil {
		s.logTimerWarn("stopTimerOnTask: find open entry", actorID, taskID, err)
		return
	}
	if open == nil || open.TaskID != taskID {
		return
	}
	if !closeOpenEntry(ctx, s.Time, open) {
		return
	}
}

// closeOpenEntry stamps end=now, computes duration_s and persists both
// with the atomic accrual onto tasks.time_spent_s. Reports failure via
// the boolean so callers can react (stale-guard must not double-open).
func closeOpenEntry(ctx context.Context, repo TimeEntries, open *timeentry.TimeEntry) bool {
	now := time.Now().UTC()
	open.EndedAt = &now
	dur := int64(now.Sub(open.StartedAt).Seconds())
	open.DurationS = &dur
	if err := repo.CloseAndAccrue(ctx, open); err != nil {
		// No structured logger dependency at this depth — callers log.
		_ = err
		return false
	}
	return true
}

// logTimerWarn emits a warn-level log when the service carries a
// logger; nil logger is fine (partial fixtures).
func (s *Service) logTimerWarn(msg, actorID, taskID string, err error) {
	if s.Logger == nil {
		return
	}
	s.Logger.Warn(msg,
		zap.String("task_id", taskID),
		zap.String("actor_id", actorID),
		zap.Error(err),
	)
}
