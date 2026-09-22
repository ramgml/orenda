// Package task — T356: the spent-time fallback on the task read
// surface.
//
// Tasks that predate the Task 87 auto-timer never got time_entries
// rows, so their stored time_spent_s stays 0 even though the status
// audit log records every in_progress stay. When a task has no
// entries at all AND a zero stored counter, the task service stamps a
// DERIVED value onto the in-memory task model at read time:
//
//	derived = sum of CLOSED in_progress intervals from the
//	          task.status_changed audit timeline
//
// The stamp is virtual — nothing is persisted back into tasks or
// time_entries. The derivation lives in the timeentry domain package
// (pure functions); this file only orchestrates two batched reads
// (status timelines + the entries-existence gate) so the kanban
// listing stays N+1-free.
package task

import (
	"context"

	"go.uber.org/zap"

	"github.com/ramgml/orenda/internal/domain/task"
	"github.com/ramgml/orenda/internal/domain/timeentry"
)

// SpentFallback is the read-side seam the task service needs to
// derive spent time for entry-less tasks. Split from TimeEntries on
// purpose: that interface is the auto-timer's write surface, while
// this one is a read-side concern that must not bloat the timer
// contract. *sqlite.ActivityRepo + *sqlite.TimeEntryRepo are adapted
// by the wiring in main.go.
type SpentFallback interface {
	// StatusChanges returns ordered status_changed events for the
	// given ids (nil = every task with usable rows).
	StatusChanges(ctx context.Context, taskIDs []string) (map[string][]timeentry.StatusSpentEvent, error)
	// HasAnyEntries reports which of the given ids own at least one
	// time_entries row.
	HasAnyEntries(ctx context.Context, taskIDs []string) (map[string]bool, error)
}

// SpentFallbackAdapter adapts the sqlite repositories to the
// SpentFallback seam. Constructed in main.go once per process.
type SpentFallbackAdapter struct {
	Statuses timeentry.StatusSpentSource
	Gate     timeentry.Repository
}

// StatusChanges adapts the activity repo's status-timeline read to
// the seam's name.
func (a SpentFallbackAdapter) StatusChanges(ctx context.Context, ids []string) (map[string][]timeentry.StatusSpentEvent, error) {
	return a.Statuses.StatusChangesByTasks(ctx, ids)
}

// HasAnyEntries adapts the time-entry repo's existence probe to the
// seam's name.
func (a SpentFallbackAdapter) HasAnyEntries(ctx context.Context, ids []string) (map[string]bool, error) {
	return a.Gate.HasAnyEntriesByTasks(ctx, ids)
}

// StampDerivedSpent overwrites tr.TimeSpentS with the status-derived
// value for tasks that qualify: stored counter == 0 AND no
// time_entries rows. Everything else keeps its stored value.
//
// Contract (T356): a manually set counter (>0 via PATCH) naturally
// disables the fallback, and a zero counter WITH entries stays zero
// (the entries exist but sum to nothing — do not invent time).
//
// Batched for listings: one StatusChanges + one HasAnyEntries call
// for the whole input slice, no per-task queries. nil-safe: without
// the Spent dependency (partial fixtures, tests) it is a no-op, and
// failures degrade to the stored values — a read must never fail
// because the fallback glitched.
func (s *Service) StampDerivedSpent(ctx context.Context, tasks []*task.Task) {
	if s == nil || s.Spent == nil || len(tasks) == 0 {
		return
	}
	// Candidates: only zero-counter tasks can possibly qualify.
	ids := make([]string, 0, len(tasks))
	for _, tr := range tasks {
		if tr != nil && tr.TimeSpentS == 0 {
			ids = append(ids, tr.ID)
		}
	}
	if len(ids) == 0 {
		return
	}
	hasEntries, err := s.Spent.HasAnyEntries(ctx, ids)
	if err != nil {
		s.logWarn("spent fallback: entries gate failed", err)
		return
	}
	// Timelines only for the ids that passed the gate.
	fallbackIDs := make([]string, 0, len(ids))
	for _, id := range ids {
		if !hasEntries[id] {
			fallbackIDs = append(fallbackIDs, id)
		}
	}
	if len(fallbackIDs) == 0 {
		return
	}
	statuses, err := s.Spent.StatusChanges(ctx, fallbackIDs)
	if err != nil {
		s.logWarn("spent fallback: status timeline read failed", err)
		return
	}
	for _, tr := range tasks {
		if tr == nil || tr.TimeSpentS != 0 || hasEntries[tr.ID] {
			continue
		}
		if derived := timeentry.DeriveSpentSeconds(statuses[tr.ID]); derived > 0 {
			tr.TimeSpentS = int(derived)
		}
	}
}

// logWarn routes through the service logger when wired; silent
// otherwise (same degradation convention as syncTimer).
func (s *Service) logWarn(msg string, err error) {
	if s.Logger != nil {
		s.Logger.Warn(msg, zap.Error(err))
	}
}
