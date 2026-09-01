// Package task provides business logic on top of task.Repository. Phase 2
// introduces Move() with fractional-position kanban reordering. Phase 3
// adds Claim/Release/Submit/Review with atomic task_locks.
package task

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/ramgml/orenda/internal/api/ws"
	"github.com/ramgml/orenda/internal/domain/activity"
	commentdomain "github.com/ramgml/orenda/internal/domain/comment"
	"github.com/ramgml/orenda/internal/domain/project"
	"github.com/ramgml/orenda/internal/domain/task"
	"github.com/ramgml/orenda/internal/storage/sqlite"
)

// Sentinel errors returned by Service. Handlers translate these into HTTP
// status codes (mirrors api.writeError semantics).
var (
	ErrNotFound         = errors.New("task service: not found")
	ErrColumnFull       = errors.New("task service: column WIP limit reached")
	ErrInvalidInput     = errors.New("task service: invalid input")
	ErrTaskBlocked      = errors.New("task service: task is blocked by unfinished dependencies")
	ErrDependencyCycle  = errors.New("task service: dependency cycle")
	ErrSelfDependency   = errors.New("task service: task cannot depend on itself")
	ErrDependencyExists = errors.New("task service: dependency already exists")
)

// BlockedError is returned by Claim when the task has unfinished
// dependencies. It carries the list of open blocker IDs so the
// caller can render "still blocked by these" instead of a generic
// 422 message.
type BlockedError struct {
	BlockerIDs []string
}

func (e *BlockedError) Error() string {
	return fmt.Sprintf("task service: task is blocked by %d unfinished dependencies", len(e.BlockerIDs))
}

// Is implements the errors.Is contract: a BlockedError matches
// ErrTaskBlocked so callers can `errors.Is(err, ErrTaskBlocked)`.
func (e *BlockedError) Is(target error) bool {
	return target == ErrTaskBlocked
}

// MoveOptions captures how a task should be placed inside the target column.
//
// Position semantics (Phase 2.4):
//
//   - (Before == nil && After == nil): append to the end of the column.
//     The service picks position = max(existing) + 1024.
//   - (Before != nil && After == nil): place after Before. position =
//     Before.position + 1024. If After in the same column is the same
//     row we skip; otherwise we renumber carefully (not implemented in
//     Phase 2.4 — fractional positions handle ~1000 inserts before a
//     renumber is needed).
//   - (After != nil && Before == nil): place before After. position =
//     After.position - 1024.
//   - (Before != nil && After != nil): place between them.
//     position = (Before.position + After.position) / 2.
//
// Position is the explicit override; if non-zero, Before/After are ignored.
//
// ActorID (Task 117) identifies who moved the card — the handler
// fills it from the session so the task.moved activity row passes
// Activity.Validate (which requires a non-empty ActorID). It only
// feeds the audit row; the empty value keeps the historical
// (silently-dropped-row) behaviour for callers without an identity.
type MoveOptions struct {
	TargetColumnID string
	Position       float64 // explicit fractional; 0 = derive from Before/After
	Before         *task.Task
	After          *task.Task
	ActorID        string
}

// Recorder is the audit hook for Claim/Release/Submit/Review. Phase 3.9
// wires it to activity.Repository.
type Recorder interface {
	Record(ctx context.Context, taskID string, actorType activity.ActorType, actorID string, action activity.Action, payload string) error
}

// TombstoneRecorder is the audit hook for hard-delete (retract) flows.
// Phase 33.2.1: RetractProposal writes a row here because
// task_activity.task_id has ON DELETE CASCADE — a post-delete insert
// fails the FK or vanishes with the parent row. The tombstone table
// (migration 025) has no FK, so the audit row survives the task
// delete. nil-safe — handlers can run without a tombstone backend.
type TombstoneRecorder interface {
	RecordRetracted(ctx context.Context, taskID string, snapshotJSON string, actorType activity.ActorType, actorID string) error
}

// Hub is the in-process notification seam. Phase 2.5 wires the WebSocket
// hub; Phase 3 wires the agent inbox.
//
// We re-export ws.Hub to keep a single source of truth for the interface
// (avoids drift between api/ws.Hub and service/task.Hub).
type Hub = ws.Hub

// Service holds the dependencies Move() and future business logic need.
type Service struct {
	Tasks     task.Repository
	Locks     Locks
	Recorder  Recorder
	Tombstone TombstoneRecorder
	Comments  CommentAdder
	// CommentLister is the seam MirrorSave uses to fetch the
	// task's current comment thread for the markdown mirror. The
	// concrete implementation is *comment.Service; nil means
	// "no comments in the mirror" (no behaviour change for
	// installs that don't wire it).
	CommentLister CommentLister
	Hub           Hub
	Mirror        MirrorWriter
	// Columns exposes the project columns repository — used by
	// Move() to (a) check WIP limits (Phase 23.1) and (b) resolve
	// the project that owns the target column so an Inbox card
	// dragged onto a real board gets filed under that project
	// (Phase 16.7).
	Columns project.Repository
	// Time is the auto-timer backend (Task 87): when wired, every
	// status transition opens/closes a time entry for the actor so
	// tracked time flows from the task lifecycle instead of a manual
	// timer. nil-safe — the timer simply stays off.
	Time TimeEntries
	// Logger is used for warn/error logs in the service layer.
	// nil-safe — callers that don't set it get silent degradation.
	Logger *zap.Logger
}

// MirrorWriter is the seam for the Phase 7 markdown mirror. The concrete
// implementation is internal/mirror.Service; nil means "no mirror".
//
// Phase 13 added the tags slice to WriteTask so the markdown frontmatter
// carries the label vocabulary. Callers that don't have tags handy
// pass nil; the frontmatter omits the field entirely.
type MirrorWriter interface {
	WriteTask(t *task.Task, checklists []task.Checklist, itemsByList map[string][]task.ChecklistItem, comments []*commentdomain.Comment, tags []task.Tag) (string, error)
	DeleteTask(id string) error
}

// New returns a Service. Tasks is mandatory; other fields can be nil and
// the corresponding operations will return an explicit "no backend" error
// or skip the side effect.
func New(tasks task.Repository, locks Locks, recorder Recorder, comments CommentAdder, hub Hub) *Service {
	return &Service{
		Tasks:    tasks,
		Locks:    locks,
		Recorder: recorder,
		Comments: comments,
		Hub:      hub,
	}
}

// NewWithTombstone wires the Phase 33.2.1 retract-tombstone recorder
// into the Service. The retract flow needs a TombstoneRecorder to
// persist audits that survive the task row (task_activity.task_id
// has ON DELETE CASCADE).
func NewWithTombstone(tasks task.Repository, locks Locks, recorder Recorder, tombstone TombstoneRecorder, comments CommentAdder, hub Hub) *Service {
	s := New(tasks, locks, recorder, comments, hub)
	s.Tombstone = tombstone
	return s
}

// lookupColumnForStatus returns the column id on the (single) board of
// tr.ProjectID whose status matches status. Returns "" when no such
// column exists; callers fall back to "no change" so the side effect
// doesn't break a PATCH that just wanted to set the label.
//
// Phase 27.8 collapses status and column_id into a single axis — a
// PATCH on one must update the other. We do that here so every entry
// point (move, applyTaskPatch, the agent-flow methods below) sees
// the same canonical pair.
//
// Errors from FindColumnByStatus are deliberately swallowed: callers
// treat "no column for this status" as a non-error (a card with
// archived status, or a project that hasn't created a column for a
// custom status yet). The contract "best-effort lookup, default to
// empty" is intentional — see syncColumnToStatus callers below.
func (s *Service) lookupColumnForStatus(ctx context.Context, projectID, status string) string {
	if s.Columns == nil || status == "" || projectID == "" {
		return ""
	}
	col, err := s.Columns.FindColumnByStatus(ctx, projectID, status)
	if err != nil {
		if s.Logger != nil {
			s.Logger.Warn("lookupColumnForStatus: FindColumnByStatus failed",
				zap.String("project_id", projectID),
				zap.String("status", status),
				zap.Error(err),
			)
		}
		return ""
	}
	return col.ID
}

// syncColumnToStatus keeps the (task.status, task.column_id) pair in
// sync: when status changes, the column is moved to the column that
// carries the new status. Used by every write path that touches
// status — agent-flow Claim/Submit/Review, the user-side applyTaskPatch,
// and Move when the user dragged a card to a column with a different
// status.
//
// The column id is updated in-place on tr (via the repo Update call
// at the call site), so callers don't need a separate return value
// to emit a single activity row.
func (s *Service) syncColumnToStatus(ctx context.Context, tr *task.Task) {
	if s.Columns == nil || tr.ProjectID == "" {
		return
	}
	colID := s.lookupColumnForStatus(ctx, tr.ProjectID, string(tr.Status))
	if colID == "" {
		// Status has no matching column on this board (custom
		// status, off-board task, …). Keep the column the user
		// already picked — the status label changes but the visual
		// position doesn't, which is the right UX for a custom
		// status without a column.
		return
	}
	tr.ColumnID = colID
}

// SyncStatusAndColumn is the public surface of the same logic —
// handlers call it after applyTaskPatch to keep the invariant on
// every PATCH. It is safe to call with nil tr (no-op) or with a
// task whose project has no columns (also no-op).
//
// We expose this as a separate method rather than embedding the
// sync inside applyTaskPatch because applyTaskPatch lives in the
// api package (it mutates the task struct from a wire shape) and
// the lookup needs the service's column repo — which is the
// service's dependency, not the handler's.
func (s *Service) SyncStatusAndColumn(ctx context.Context, tr *task.Task) {
	if s == nil || tr == nil {
		return
	}
	s.syncColumnToStatus(ctx, tr)
}

// SyncAndSave is the canonical save point for every PATCH that may touch
// status or column_id. It performs the status→column sync ONLY when the
// status actually changed (tr.Status != prevStatus), persists the task,
// mirrors it, and records a task.status_changed activity row.
//
// prevStatus is the task's status BEFORE the caller mutated it — the
// caller (applyTaskPatchAndEffects) captures it before applying changes.
// This lets SyncAndSave detect the transition and record the activity.
//
// Column→status (the reverse direction, e.g. user drags a card) is done
// by the caller BEFORE calling SyncAndSave — the caller knows which axis
// the user changed. SyncAndSave NEVER touches the column when status
// hasn't changed, so an explicit column_id PATCH persists even when the
// column has no status (statusless columns) or the row was pre-diverged.
//
// actorType identifies who performed the action (ActorUser, ActorAgent,
// ActorSystem). The sync path (offline outbox) should use ActorSystem.
func (s *Service) SyncAndSave(ctx context.Context, tr *task.Task, actorID string, actorType activity.ActorType, prevStatus task.Status) error {
	if s == nil || tr == nil {
		return nil
	}

	// status → column: ONLY when status actually changed. When only
	// column_id changed (explicit drag or statusless column), the caller
	// already set both column_id and status — don't overwrite.
	if tr.Status != prevStatus {
		s.syncColumnToStatus(ctx, tr)
	}

	if err := s.Tasks.Update(ctx, tr); err != nil {
		return err
	}
	s.mirrorSave(ctx, tr)

	if s.Recorder != nil && tr.Status != prevStatus {
		_ = s.Recorder.Record(ctx, tr.ID, actorType, actorID, activity.ActionStatusChanged,
			fmt.Sprintf(`{"from":%q,"to":%q}`, prevStatus, tr.Status))
	}
	// Task 87: PATCH/kanban-move is a first-class transition point —
	// entering in_progress opens the actor's entry, leaving closes
	// it. Uses the recorded prevStatus; no-op when nothing changed.
	s.syncTimer(ctx, tr, prevStatus)
	return nil
}

// Move relocates a task to a new column / position atomically.
//
// The repository update is performed in a single transaction (using the
// underlying *sql.DB via RepoWithTx; if the repo doesn't support it we
// fall back to a non-transactional best-effort write — see sqlite impl
// for the canonical path).
//
// Returns ErrColumnFull when the target column has a WIP limit and the
// move would exceed it (counted excluding the moved task if it's already
// in the same column).
func (s *Service) Move(ctx context.Context, taskID string, opts MoveOptions) (*task.Task, error) {
	if taskID == "" || opts.TargetColumnID == "" {
		return nil, ErrInvalidInput
	}

	tr, err := s.Tasks.GetByID(ctx, taskID)
	if err != nil {
		return nil, ErrNotFound
	}
	prevStatus := tr.Status

	// WIP-limit check (count tasks in target column, excluding self).
	if limit, ok := s.lookupWIPLimit(ctx, opts.TargetColumnID); ok && limit > 0 {
		existing, lerr := s.Tasks.ListByProject(ctx, task.Filter{ColumnID: opts.TargetColumnID})
		if lerr != nil {
			return nil, fmt.Errorf("task service: list column: %w", lerr)
		}
		count := 0
		for _, t := range existing {
			if t.ID != taskID {
				count++
			}
		}
		if count >= limit {
			return nil, ErrColumnFull
		}
	}

	tr.ColumnID = opts.TargetColumnID
	tr.Position = derivePosition(opts, tr.Position)

	// Phase 16: dragging an Inbox card onto a project's board files
	// it under that project. We resolve the project id from the
	// target column's board via the columns repository (which knows
	// the project_id → column_id mapping). When tr.ProjectID is
	// already set (a normal intra-project move), this is a no-op.
	if tr.ProjectID == "" {
		if pid, ok := s.lookupProjectOfColumn(ctx, opts.TargetColumnID); ok {
			tr.ProjectID = pid
		}
	}

	// Phase 27.8: collapse the two axes (status, column) into one —
	// the column's status is the source of truth when the user picked
	// a destination column by dragging. The reverse direction
	// (status → column) is handled by syncColumnToStatus / SyncStatusAndColumn
	// on every write that touches status. Move is the only path that
	// moves by column alone, so it has to lift the column's status
	// onto the task here. Defensive: nil Columns repo or missing
	// status key leaves the task's status untouched (the old behaviour).
	// Task 117: the same single lookup also resolves the target
	// column's NAME for the task.moved activity payload — the feed
	// shows "→ In Review" instead of a raw column UUID. Lookup
	// failure leaves columnName empty and the payload is written in
	// the legacy (column_id-only) shape, so old feed readers and the
	// activityDetails fallback keep working unchanged.
	var columnName string
	if s.Columns != nil {
		if col, err := s.Columns.GetColumn(ctx, opts.TargetColumnID); err == nil {
			if col.Status != "" {
				tr.Status = task.Status(col.Status)
			}
			columnName = col.Name
		}
	}

	// Phase 33.1 + 33.3: defensive clearing of awaiting=human on a
	// move to a non-review column. The propose handler no longer
	// stamps awaiting=human (Phase 33.3: backlog tasks are triaged on
	// the board, not in the review queue), so this branch is
	// effectively only hit by legacy rows and by user-created
	// awaiting=human cards. Keeping the logic intact is the right
	// call — it's the safety net for any existing awaiting=human row
	// that gets dragged onto the board.
	if tr.Awaiting == task.AwaitingHuman && tr.Status != task.StatusReview {
		tr.Awaiting = task.AwaitingNone
	}

	if err := s.Tasks.Update(ctx, tr); err != nil {
		return nil, fmt.Errorf("task service: update: %w", err)
	}
	s.mirrorSave(ctx, tr)
	// Task 87: a kanban drag that crosses the in_progress boundary
	// opens/closes the actor's auto-timer entry.
	s.syncTimer(ctx, tr, prevStatus)

	if s.Recorder != nil {
		payload := map[string]any{
			"column_id": opts.TargetColumnID,
			"position":  tr.Position,
		}
		// Task 117: column_name is written only when the lookup
		// succeeded — legacy rows (and any failure) keep the old
		// payload shape, which the frontend falls back to UUID on.
		if columnName != "" {
			payload["column_name"] = columnName
		}
		raw, _ := json.Marshal(payload) // map[string]any of basic values cannot fail
		_ = s.Recorder.Record(ctx, taskID, activity.ActorUser, opts.ActorID, activity.ActionMoved,
			string(raw))
	}
	if s.Hub != nil {
		s.Hub.Publish(ctx, ws.Event{
			Topic: "tasks",
			Body: map[string]any{
				"type":   "task.moved",
				"task":   tr,
				"column": opts.TargetColumnID,
			},
		})
	}
	return tr, nil
}

// lookupWIPLimit returns the WIP limit set on a column, or (0, false)
// if the column has no limit / doesn't exist / the Columns repo
// isn't wired. Phase 23.1 turns the long-standing stub into the real
// implementation: columns.wip_limit is read once per Move() call
// (cheap PK lookup, no caching needed).
func (s *Service) lookupWIPLimit(ctx context.Context, columnID string) (int, bool) {
	if s.Columns == nil || columnID == "" {
		return 0, false
	}
	col, err := s.Columns.GetColumn(ctx, columnID)
	if err != nil || col == nil || col.WIPLimit == nil {
		return 0, false
	}
	return *col.WIPLimit, true
}

// lookupProjectOfColumn returns the project id that owns a column.
// Used by Move() to file an Inbox card under a real project when it's
// dragged onto one of that project's boards (Phase 16.7).
func (s *Service) lookupProjectOfColumn(ctx context.Context, columnID string) (string, bool) {
	if s.Columns == nil || columnID == "" {
		return "", false
	}
	col, err := s.Columns.GetColumn(ctx, columnID)
	if err != nil || col == nil {
		return "", false
	}
	return col.ProjectID, true
}

// derivePosition resolves the new position from opts.
func derivePosition(opts MoveOptions, current float64) float64 {
	if opts.Position != 0 {
		return opts.Position
	}
	switch {
	case opts.Before != nil && opts.After != nil:
		return (opts.Before.Position + opts.After.Position) / 2
	case opts.Before != nil:
		return opts.Before.Position + 1024
	case opts.After != nil:
		return opts.After.Position - 1024
	default:
		// Append: the repository will assign the next free slot via
		// (max+1024). For Move we keep the current value; the caller
		// can pass an explicit position if they care.
		return current + 1024
	}
}

// nullHub is the no-op Hub used when none is configured.
type nullHub struct{}

func (nullHub) Publish(context.Context, ws.Event) {}
func (nullHub) Close()                            {}
func (nullHub) Subscribe(string, string) (<-chan ws.Event, ws.Unsubscribe) {
	ch := make(chan ws.Event)
	close(ch)
	return ch, func() {}
}

// NullHub returns a Hub that drops every event. Useful in tests and
// during `migrate status` where the WS hub isn't running.
func NullHub() Hub { return nullHub{} }

// ----------------------------------------------------------------------------
// Phase 3.6: Claim / Release / Submit / Review
// ----------------------------------------------------------------------------

// Locks is the small surface Claim/Release/Submit need to interact with
// the task_locks table. *sqlite.taskLockRepo satisfies it.
type Locks interface {
	Acquire(ctx context.Context, taskID, agentID string) error
	Release(ctx context.Context, taskID, agentID string) error
	Holder(ctx context.Context, taskID string) (agentID string, acquiredAt time.Time, err error)
}

// Claim assigns a task to an agent atomically.
//
// Steps (wrapped in best-effort transaction for Phase 3 — full SQLite
// serialisable transaction support lands when we add a tx-aware repo
// interface in Phase 9):
//
//  1. Acquire task_locks(task_id, agent_id). UNIQUE PK → ErrLockTaken
//     if another agent already holds it.
//  2. Update tasks: assignee_type='agent', assignee_id=agentID,
//     status=in_progress, claimed_at=now, started_at=now, awaiting=none.
//  3. Audit + WS event.
//
// Returns the updated task.
func (s *Service) Claim(ctx context.Context, taskID, agentID string) (*task.Task, error) {
	if s.Locks == nil {
		return nil, errors.New("task service: Claim requires a Locks backend")
	}

	// Phase 15: refuse to claim a task whose dependencies aren't all
	// 'done'. We check this BEFORE acquiring the lock so a denied
	// claim doesn't leave a stale lock row behind. The list of
	// unfinished blockers goes onto the error so the agent can show
	// the user "still blocked by these".
	blockers, err := s.Tasks.Blockers(ctx, taskID)
	if err != nil {
		return nil, fmt.Errorf("task service: Claim: blockers: %w", err)
	}
	var openBlockers []task.BlockerRow
	for _, b := range blockers {
		if !b.Done {
			openBlockers = append(openBlockers, b)
		}
	}
	if len(openBlockers) > 0 {
		ids := make([]string, len(openBlockers))
		for i, b := range openBlockers {
			ids[i] = b.BlockerID
		}
		return nil, &BlockedError{BlockerIDs: ids}
	}

	if err := s.Locks.Acquire(ctx, taskID, agentID); err != nil {
		if errors.Is(err, sqlite.ErrLockTaken) {
			return nil, ErrLockTaken
		}
		if errors.Is(err, sqlite.ErrLockNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}

	tr, err := s.Tasks.GetByID(ctx, taskID)
	if err != nil {
		// Translate the domain's ErrNotFound into the service's so handlers
		// can match on a single sentinel. Best-effort lock rollback either
		// way so the agent can retry.
		_ = s.Locks.Release(ctx, taskID, agentID)
		if errors.Is(err, task.ErrNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}

	now := time.Now()
	tr.AssigneeType = task.AssigneeAgent
	tr.AssigneeID = agentID
	tr.Status = task.StatusInProgress
	tr.Awaiting = task.AwaitingNone
	tr.StartedAt = &now
	tr.ClaimedAt = &now
	// Phase 27.8: status ≡ column. Keep the card on the column that
	// carries "in_progress" so the kanban reflects the agent's
	// claim without a separate move.
	s.syncColumnToStatus(ctx, tr)

	if err := s.Tasks.Update(ctx, tr); err != nil {
		_ = s.Locks.Release(ctx, taskID, agentID)
		return nil, err
	}
	s.mirrorSave(ctx, tr)

	if s.Recorder != nil {
		_ = s.Recorder.Record(ctx, taskID, activity.ActorAgent, agentID, activity.ActionClaimed, "")
	}
	s.publishTask(ctx, "task.claimed", tr, agentID, map[string]any{"agent_id": agentID})
	// Task 87: the claim flips the task into in_progress — the
	// auto-timer opens an entry for the new assignee.
	s.syncTimer(ctx, tr, task.StatusTodo)
	return tr, nil
}

// Release un-assigns a task that the agent currently holds.
func (s *Service) Release(ctx context.Context, taskID, agentID string) (*task.Task, error) {
	if s.Locks == nil {
		return nil, errors.New("task service: Release requires a Locks backend")
	}
	if err := s.Locks.Release(ctx, taskID, agentID); err != nil {
		if errors.Is(err, sqlite.ErrLockNotHeld) {
			return nil, ErrLockNotHeld
		}
		return nil, err
	}

	tr, err := s.Tasks.GetByID(ctx, taskID)
	if err != nil {
		return nil, err
	}
	// Task 87: release drops the task to todo and lets go of the
	// work — the auto-timer closes the agent's open entry so the
	// interval is attributed and accrued rather than orphaned. The
	// actor is the releasing agent even though the row's assignee is
	// being cleared; the mutation lands first so the transition
	// (in_progress → todo) is visible to the timer sync.
	tr.AssigneeType = ""
	tr.AssigneeID = ""
	tr.Status = task.StatusTodo
	tr.Awaiting = task.AwaitingNone
	s.syncTimerAs(ctx, tr, task.StatusInProgress, agentID)
	// Phase 27.8: drop the card back onto the "todo" column.
	s.syncColumnToStatus(ctx, tr)
	// Task 92: persist the release through the partial UPDATE — the
	// timer sync above accrued a closed entry (+time_spent_s) via an
	// atomic relative UPDATE behind this function's read; the
	// pre-92 full-row Update rewrote time_spent_s from that stale
	// read, so an accrual committing between the re-read and the
	// write was silently lost. ClearAssigneeToTodo never touches the
	// counter, so the accrual survives. The in-memory tr keeps its
	// stale TimeSpentS for the caller; the mirror and the publish
	// below read the authoritative row.
	if err := s.Tasks.ClearAssigneeToTodo(ctx, tr.ID, tr); err != nil {
		return nil, err
	}
	if fresh, ferr := s.Tasks.GetByID(ctx, tr.ID); ferr == nil {
		tr.TimeSpentS = fresh.TimeSpentS
	}
	s.mirrorSave(ctx, tr)

	if s.Recorder != nil {
		_ = s.Recorder.Record(ctx, taskID, activity.ActorAgent, agentID, activity.ActionReleased, "")
	}
	s.publishTask(ctx, "task.released", tr, agentID, map[string]any{"agent_id": agentID})
	return tr, nil
}

// SetTaskDependencies replaces a task's full blocker set.
//
// We run a DFS-based cycle check before the swap: a cycle would mean
// a task transitively depends on itself, which Claim() can't enforce
// (the cycle may only surface after the new edges are in). The DFS
// treats dependsOnIDs as the new outgoing edges from `taskID` and
// asks: does any of them reach back to taskID through the existing
// graph plus the new edges?
//
// Empty dependsOnIDs clears every blocker (use case: split a task —
// once split, the orphan blockers should disappear).
func (s *Service) SetTaskDependencies(ctx context.Context, taskID string, dependsOnIDs []string) error {
	if taskID == "" {
		return ErrInvalidInput
	}
	// Reject direct self-loops early.
	for _, dep := range dependsOnIDs {
		if dep == "" {
			continue
		}
		if dep == taskID {
			return ErrSelfDependency
		}
	}
	// De-dupe (the caller may pass the same id twice; we don't
	// care but the DFS would loop).
	seen := make(map[string]struct{}, len(dependsOnIDs))
	cleaned := make([]string, 0, len(dependsOnIDs))
	for _, dep := range dependsOnIDs {
		if dep == "" {
			continue
		}
		if _, dup := seen[dep]; dup {
			continue
		}
		seen[dep] = struct{}{}
		cleaned = append(cleaned, dep)
	}

	// Confirm the target task exists. If not, every depends-on id check
	// below returns false anyway; we want a clean ErrNotFound up front.
	if _, err := s.Tasks.GetByID(ctx, taskID); err != nil {
		if errors.Is(err, task.ErrNotFound) {
			return ErrNotFound
		}
		return err
	}

	// Cycle check: build an adjacency map of "current graph + new edges"
	// (capped to nodes we actually care about — the deps of deps), then
	// DFS from each new edge looking for taskID.
	if err := s.checkDependencyCycles(ctx, taskID, cleaned); err != nil {
		return err
	}

	if err := s.Tasks.SetTaskDependencies(ctx, taskID, cleaned); err != nil {
		// Translate domain sentinels so the handler only deals with
		// service-level errors.
		if errors.Is(err, task.ErrSelfDependency) {
			return ErrSelfDependency
		}
		return err
	}
	// Fire WS event so other tabs refresh their badge/blockers view.
	if s.Hub != nil {
		s.Hub.Publish(ctx, ws.Event{Topic: "tasks", Body: map[string]any{
			"type":       "task.deps_changed",
			"task_id":    taskID,
			"depends_on": cleaned,
		}})
	}
	return nil
}

// checkDependencyCycles walks the dependency graph (starting from
// each proposed edge) and returns ErrDependencyCycle if any path
// leads back to taskID.
//
// We only fetch the edges once and reuse — the graph is small in
// practice (single user, focused projects). For pathological cases
// we'd bound the visit count.
func (s *Service) checkDependencyCycles(ctx context.Context, taskID string, proposedEdges []string) error {
	if len(proposedEdges) == 0 {
		return nil
	}
	// Gather every task id we'll need to look up edges for: the
	// proposed nodes plus whatever they transitively depend on.
	seeds := append([]string{}, proposedEdges...)
	visited := make(map[string]bool)
	edges := make(map[string][]string) // from -> [to]

	queue := append([]string{}, seeds...)
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		if visited[cur] {
			continue
		}
		visited[cur] = true
		// Direct edges from cur.
		blockers, err := s.Tasks.Blockers(ctx, cur)
		if err != nil {
			return fmt.Errorf("checkDependencyCycles: blockers of %s: %w", cur, err)
		}
		var next []string
		for _, b := range blockers {
			next = append(next, b.BlockerID)
		}
		edges[cur] = next
		queue = append(queue, next...)
	}

	// DFS from each proposed edge: if we ever land on taskID, cycle.
	const maxSteps = 1024
	for _, start := range proposedEdges {
		stack := []string{start}
		seen := make(map[string]bool)
		steps := 0
		for len(stack) > 0 {
			steps++
			if steps > maxSteps {
				// Cycle-ish: bail rather than spin.
				return ErrDependencyCycle
			}
			cur := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			if cur == taskID {
				return ErrDependencyCycle
			}
			if seen[cur] {
				continue
			}
			seen[cur] = true
			stack = append(stack, edges[cur]...)
		}
	}
	return nil
}

// Submit marks a task as ready for human review (status=review,
// awaiting=human).
func (s *Service) Submit(ctx context.Context, taskID, agentID, note string) (*task.Task, error) {
	// Verify the agent still holds the lock before flipping the status.
	if s.Locks != nil {
		// We don't fail hard on missing lock — Phase 3 schema enforces
		// ON DELETE CASCADE so a released task may have no lock yet.
		// Trust the caller; downstream review enforces human check.
		_ = s.Locks
	}

	tr, err := s.Tasks.GetByID(ctx, taskID)
	if err != nil {
		return nil, err
	}
	tr.Status = task.StatusReview
	tr.Awaiting = task.AwaitingHuman
	if note != "" {
		tr.AgentNotes = note
	}
	// Phase 27.8: move the card onto the "review" column.
	s.syncColumnToStatus(ctx, tr)
	if err := s.Tasks.Update(ctx, tr); err != nil {
		return nil, err
	}
	s.mirrorSave(ctx, tr)

	// Task 87: submit moves the task out of in_progress (→ review)
	// — the auto-timer closes the agent's entry and accrues it.
	s.syncTimerAs(ctx, tr, task.StatusInProgress, agentID)
	s.publishTask(ctx, "task.submitted", tr, agentID, map[string]any{
		"agent_id": agentID,
		"note":     note,
	})
	if s.Recorder != nil {
		_ = s.Recorder.Record(ctx, taskID, activity.ActorAgent, agentID, activity.ActionSubmitted, note)
	}
	return tr, nil
}

// ReviewDecision enumerates the two valid review outcomes.
type ReviewDecision string

const (
	ReviewApprove ReviewDecision = "approve"
	ReviewReject  ReviewDecision = "reject"
)

// Review approves or rejects a task in review.
//
//	approve → status=done, completed_at=now, awaiting=none.
//	reject  → status=in_progress, awaiting=agent (back to the agent).
//
// If comment is non-empty, a comment row is added via the CommentRepo
// (Phase 3.7 wires the full CommentRepo; Phase 3.11 hands the service
// one as an optional dependency).
func (s *Service) Review(ctx context.Context, taskID, userID string, decision ReviewDecision, comment string) (*task.Task, error) {
	if decision != ReviewApprove && decision != ReviewReject {
		return nil, ErrInvalidInput
	}
	// Phase 30.7: reject without a comment is a reject without a
	// reason — the agent doesn't know what to fix. Approve is
	// allowed without one (some approvals are silent ack).
	if decision == ReviewReject && strings.TrimSpace(comment) == "" {
		return nil, ErrInvalidInput
	}
	tr, err := s.Tasks.GetByID(ctx, taskID)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	switch decision {
	case ReviewApprove:
		tr.Status = task.StatusDone
		tr.CompletedAt = &now
		tr.Awaiting = task.AwaitingNone
	case ReviewReject:
		tr.Status = task.StatusInProgress
		tr.Awaiting = task.AwaitingAgent
	}
	// Phase 27.8: status → column sync. Done/Approve drops the
	// card on the "done" column; Reject sends it back to the
	// "in_progress" column.
	s.syncColumnToStatus(ctx, tr)
	if comment != "" && s.Comments != nil {
		if _, cerr := s.Comments.Add(ctx, &CommentInput{
			TargetType: commentTargetTask,
			TargetID:   taskID,
			AuthorType: commentAuthorUser,
			AuthorID:   userID,
			BodyMD:     comment,
		}); cerr != nil {
			return nil, fmt.Errorf("task service: Review: comment: %w", cerr)
		}
	}
	if err := s.Tasks.Update(ctx, tr); err != nil {
		return nil, err
	}
	s.mirrorSave(ctx, tr)
	if s.Recorder != nil {
		_ = s.Recorder.Record(ctx, taskID, activity.ActorUser, userID, activity.ActionReviewed,
			fmt.Sprintf(`{"decision":%q}`, decision))
	}
	// Task 87: the review decision flips the status both ways —
	// approve (→ done) closes the assignee's open entry; reject
	// (→ in_progress) re-opens the timer so rework time is tracked.
	// The actor is the assignee when agent-assigned, otherwise the
	// project owner (single-owner installs).
	s.syncTimer(ctx, tr, task.StatusReview)
	s.publishTask(ctx, "task.reviewed", tr, userID, map[string]any{
		"decision": string(decision),
		"comment":  comment,
	})
	return tr, nil
}

// publishTask emits a WS event for task mutations.
func (s *Service) publishTask(ctx context.Context, eventType string, tr *task.Task, actorID string, extra map[string]any) {
	if s.Hub == nil {
		return
	}
	body := map[string]any{
		"type":  eventType,
		"task":  tr,
		"actor": actorID,
	}
	for k, v := range extra {
		body[k] = v
	}
	s.Hub.Publish(ctx, ws.Event{Topic: "tasks", Body: body})
}

// MirrorSave writes the task to the markdown mirror (Phase 7.2 wiring).
// Best-effort: failures are ignored — the next push will catch up.
// Exported so handlers that bypass the service (PATCH /tasks/:id) can
// still trigger the mirror.
//
// Phase 13: fetches the task's current tags and threads them into the
// mirror frontmatter. Each tag fetch failure is swallowed — the next
// push run will re-fetch and pick up the latest set.
//
// Phase Wave 4 PR 2: also fetches the task's comment thread so the
// markdown mirror carries the discussion. Without this the audit's
// "mirror не пишет комментарии" gap stays open. Nil CommentLister
// is OK — the section is just omitted.
func (s *Service) MirrorSave(ctx context.Context, tr *task.Task) {
	if s.Mirror == nil || tr == nil {
		return
	}
	rows, _ := s.Tasks.ListChecklists(ctx, tr.ID)
	cls := make([]task.Checklist, 0, len(rows))
	itemsByList := map[string][]task.ChecklistItem{}
	for _, r := range rows {
		cls = append(cls, task.Checklist{
			ID:       r.ID,
			TaskID:   r.TaskID,
			Title:    r.Title,
			Position: r.Position,
		})
		its, _ := s.Tasks.ListChecklistItems(ctx, r.ID)
		conv := make([]task.ChecklistItem, 0, len(its))
		for _, it := range its {
			conv = append(conv, task.ChecklistItem(it))
		}
		itemsByList[r.ID] = conv
	}
	tags, _ := s.Tasks.ListTagsForTask(ctx, tr.ID)
	// Phase Wave 4 PR 2: pull the comment thread so the mirror
	// shows the discussion. A failure to fetch is non-fatal — the
	// next push re-tries.
	var comments []*commentdomain.Comment
	if s.CommentLister != nil {
		comments, _ = s.CommentLister.ListByTarget(ctx, commentdomain.TargetTask, tr.ID)
	}
	_, _ = s.Mirror.WriteTask(tr, cls, itemsByList, comments, tags)
}

// mirrorSave is the internal alias kept for service-internal callers.
func (s *Service) mirrorSave(ctx context.Context, tr *task.Task) {
	s.MirrorSave(ctx, tr)
}

// MirrorDelete removes the mirror file. Called from the Delete handler
// (which currently lives in handlers_tasks.go — Phase 9 may move it here).
func (s *Service) MirrorDelete(id string) {
	if s.Mirror == nil {
		return
	}
	_ = s.Mirror.DeleteTask(id)
}

// RecordActivity writes a single task_activity row. Phase 14 entry
// point so handlers can emit child-task / checklist events through
// the service without needing the activity repo injected directly.
// Best-effort: errors are swallowed so audit glitches never block
// the user-facing write.
func (s *Service) RecordActivity(ctx context.Context, taskID, actorID string, action activity.Action, payload string) {
	if s.Recorder == nil || taskID == "" {
		return
	}
	_ = s.Recorder.Record(ctx, taskID, activity.ActorUser, actorID, action, payload)
}

// ----------------------------------------------------------------------------
// Cross-service seams used by Review (Phase 3.7 will wire the real impl).
// ----------------------------------------------------------------------------

// CommentInput is the seam used by Review to create a comment row.
type CommentInput struct {
	TargetType string
	TargetID   string
	AuthorType string
	AuthorID   string
	BodyMD     string
}

const (
	commentTargetTask = "task"
	commentAuthorUser = "user"
)

// CommentAdder is the tiny surface Review needs to attach a rejection
// comment. *comment.Service satisfies it.
type CommentAdder interface {
	Add(ctx context.Context, in *CommentInput) (string, error)
}

// CommentLister is the tiny surface MirrorSave uses to fetch
// the task's comments. *comment.Service satisfies it; nil is
// OK (the mirror omits the section).
type CommentLister interface {
	ListByTarget(ctx context.Context, targetType commentdomain.TargetType, targetID string) ([]*commentdomain.Comment, error)
}

// Sentinel errors returned by Claim/Release/Submit/Review.
var (
	ErrLockTaken   = errors.New("task service: lock already held by another agent")
	ErrLockNotHeld = errors.New("task service: lock not held by this agent")
)
