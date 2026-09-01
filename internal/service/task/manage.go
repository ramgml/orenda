// Phase 33.2: agent-side task management.
//
// The agent owns two write surfaces that the user-side flows do not:
//
//   - Edit / retract a proposal the agent filed (status=backlog,
//     awaiting=human, created_by=me). The owner has not triaged it yet,
//     so it is still the agent's to refine.
//   - Update agent_notes on a task the agent currently holds. Notes
//     are a per-claim scratchpad — only the claim holder may write
//     them. Non-holders use comments for cross-talk.
//
// All other writes (move to a different column, change assignee,
// finish the task) stay out of scope: those are owner-driven review
// steps and the agent reaches them via claim/submit/release.
//
// Permission gates (the canonical source of truth lives here, not in
// the handlers — handlers translate the sentinel errors into HTTP
// status codes):
//
//	IsOwnProposal  → status=backlog + awaiting=human + created_by_agent=me
//	IsLockHolder   → task_locks(taskID).agent_id == me
//
// ErrNotOwnProposal and ErrNotLockHolder are returned on violation;
// handlers map them to 403 with a descriptive error string.
package task

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.uber.org/zap"

	"github.com/ramgml/orenda/internal/api/ws"
	"github.com/ramgml/orenda/internal/domain/activity"
	"github.com/ramgml/orenda/internal/domain/task"
)

// Sentinel errors for the manage flow. Handlers translate these to
// HTTP status codes (the same convention as ErrLockTaken /
// ErrTaskBlocked in move.go).
var (
	// ErrNotOwnProposal: PATCH/DELETE attempted on a task that is not
	// the caller's un-triaged proposal (created_by != me, or already
	// triaged past status=backlog / awaiting=human).
	ErrNotOwnProposal = errors.New("task service: not your proposal")

	// ErrNotLockHolder: agent_notes update attempted by an agent who
	// does not currently hold task_locks(taskID).
	ErrNotLockHolder = errors.New("task service: not the lock holder")

	// ErrNoPatchFields: PATCH arrived with no recognised fields — a
	// caller bug (or an empty body); we surface 400 to make it
	// obvious rather than silently 200 with no-op.
	ErrNoPatchFields = errors.New("task service: no patch fields supplied")
	// ErrConcurrentTriage: a TOCTOU race — the row's status left
	// backlog (or another gate) between the gate-check read and the
	// gated update. Mapped to HTTP 409 Conflict by the handler.
	ErrConcurrentTriage = errors.New("task service: concurrent triage")
)

// EditProposalPatch carries the fields the agent may change on a
// proposal. Pointer fields distinguish "leave alone" (nil) from
// "explicitly clear" (non-nil pointing at the zero value). The
// handler decodes the wire shape into this struct so the service
// stays transport-agnostic (mirrors the design of taskInput in the
// api package).
type EditProposalPatch struct {
	Title        *string
	Description  *string
	Priority     *task.Priority
	DueAt        *time.Time
	ParentTaskID *string
	// Task 115: full replacement blocker list — mirrors PUT
	// /dependencies semantics. Pointer-to-slice distinguishes
	// "absent" (nil → untouched) from "present" (replace the whole
	// set; empty slice → clear all). The replacement runs through the
	// same SetTaskDependencies core as every other write path, so the
	// auto-block state machine + activity rows apply here too.
	BlockedBy *[]string
}

// Change describes one field-level edit on a proposal. Returned in
// the *EditProposalDiff so the handler can build the activity payload
// without re-deriving the diff (and so the audit row reads as
// "title: A → B" rather than a blob).
type Change struct {
	Field string
	From  string
	To    string
}

// EditProposalDiff is the result of applying an EditProposalPatch.
// The handler turns it into a JSON activity payload and a task.updated
// WS event.
type EditProposalDiff struct {
	Before  *task.Task
	After   *task.Task
	Changes []Change
}

// IsOwnProposal reports whether tr is the caller's un-triaged proposal.
//
// The four predicates mirror the wiki:agent-task-management spec —
// only the original author may edit/retract, and only before triage.
//
// Phase 33.3 changed the propose handler to land proposals with
// awaiting=none (not awaiting=human as earlier spec implied); the
// user-visible meaning is the same — the owner has not moved the
// card off the backlog column yet. The gate uses status=backlog
// alone as the "un-triaged" marker; awaiting is no longer part of
// the gate.
//
// Legacy rows with a NULL created_by_id are treated as
// user-authored, so an agent can never piggy-back on a row that
// pre-dates migration 024.
func IsOwnProposal(tr *task.Task, agentID string) bool {
	// "Un-triaged" = still in backlog. Phase 33.3 flipped the
	// propose handler to land proposals with awaiting=none (not
	// awaiting=human), but the user-visible meaning is the same:
	// the owner has not moved the card off the backlog column
	// yet. Once status leaves backlog, the agent no longer owns
	// the proposal and any field-level edit must go through
	// comments.
	return tr != nil &&
		tr.CreatedByType == task.CreatorAgent &&
		tr.CreatedByID == agentID &&
		tr.Status == task.StatusBacklog
}

// IsLockHolder reports whether agentID currently holds the row in
// task_locks for the given task. Reads through s.Locks (the same
// surface Claim/Release use). Returns false on a Locks lookup error
// so a transient backend glitch never silently grants write access —
// the handler will log the error and translate the failure into a
// 403, which is the conservative answer.
func (s *Service) IsLockHolder(ctx context.Context, taskID, agentID string) bool {
	if s.Locks == nil {
		return false
	}
	holder, _, err := s.Locks.Holder(ctx, taskID)
	if err != nil {
		zap.L().Warn("manage: Holder lookup failed",
			zap.String("task_id", taskID),
			zap.String("agent_id", agentID),
			zap.Error(err))
		return false
	}
	return holder == agentID
}

// HolderAgentNotesOnly reports whether the caller may freely edit
// agent_notes on tr. The rule from wiki:agent-task-management: the
// current lock holder (agent who Claimed the task) is the only agent
// who may write agent_notes through the manage flow.
func (s *Service) HolderAgentNotesOnly(ctx context.Context, tr *task.Task, agentID string) bool {
	if !s.IsLockHolder(ctx, tr.ID, agentID) {
		return false
	}
	// Belt-and-braces: the agent_notes writer must already be
	// assigned to the task (the claim flow sets assignee_type=
	// 'agent' atomically). A row where the lock holder and the
	// assignee diverge is a stale lock (claim by one, release by
	// another) and we don't write through it.
	if tr.AssigneeType != task.AssigneeAgent || tr.AssigneeID != agentID {
		return false
	}
	return true
}

// EditProposal applies patch to taskID on behalf of agentID.
//
// The handler must call this AFTER verifying the route mounted under
// the agent namespace (cookie sessions can never reach here). The
// service re-checks the permission gate (defence in depth — a buggy
// or future handler can never bypass it).
//
// Returns ErrNotOwnProposal if the gate fails, ErrNoPatchFields if
// every supplied field pointer is nil, or any error from the repo on
// a write failure.
func (s *Service) EditProposal(ctx context.Context, taskID, agentID string, patch EditProposalPatch) (*EditProposalDiff, error) {
	if patch.isEmpty() {
		return nil, ErrNoPatchFields
	}
	// Read once for permission + before-snapshot + diff computation.
	// Phase 33.2.1: the WRITE re-asserts the gate in WHERE so a
	// concurrent owner triage cannot be clobbered (see
	// Tasks.UpdateProposalFields).
	tr, err := s.Tasks.GetByID(ctx, taskID)
	if err != nil {
		return nil, err
	}
	// Pre-check catches the "never yours / already triaged" cases
	// with a clean 403; the WHERE-gate in UpdateProposalFields
	// catches the rare TOCTOU race between this read and the
	// subsequent write.
	if !IsOwnProposal(tr, agentID) {
		return nil, ErrNotOwnProposal
	}

	before := *tr

	// Task 115: resolve + validate the replacement blocker list
	// BEFORE the field write so a bad blocked_by never leaves a
	// half-applied patch. Refs (T<N>) resolve like everywhere else;
	// unknown ids/refuse-cycles surface as the usual errors. The
	// replacement itself goes through SetTaskDependencies — the same
	// core as PUT /dependencies — so the auto-block state machine and
	// activity rows behave identically.
	if patch.BlockedBy != nil {
		resolved := make([]string, 0, len(*patch.BlockedBy))
		for _, ref := range *patch.BlockedBy {
			blocked, rerr := task.ResolveRef(ctx, s.Tasks, ref)
			if rerr != nil {
				return nil, fmt.Errorf("task.EditProposal: blocked_by %q: %w", ref, rerr)
			}
			resolved = append(resolved, blocked.ID)
		}
		if err := s.SetTaskDependencies(ctx, taskID, resolved); err != nil {
			return nil, err
		}
	}

	changes := applyEditProposalPatch(tr, patch)
	if len(changes) == 0 && patch.BlockedBy == nil {
		// No-op patch — return the current state without writing.
		return &EditProposalDiff{Before: &before, After: tr, Changes: changes}, nil
	}
	params := task.ProposalPatchParams{
		TaskID:      tr.ID,
		Gate:        task.ProposalGate{CreatedByID: agentID},
		Title:       patch.Title,
		Description: patch.Description,
		Priority:    patch.Priority,
		DueAt:       patch.DueAt,
		ParentID:    patch.ParentTaskID,
	}
	if err := s.Tasks.UpdateProposalFields(ctx, params); err != nil {
		if errors.Is(err, task.ErrNotFound) {
			return nil, ErrConcurrentTriage
		}
		return nil, fmt.Errorf("task.EditProposal: update: %w", err)
	}

	// Re-read so we have the canonical after-state for the diff /
	// WS payload / mirror. Cheap (PK lookup).
	updated, err := s.Tasks.GetByID(ctx, tr.ID)
	if err != nil {
		return nil, fmt.Errorf("task.EditProposal: re-read: %w", err)
	}
	tr = updated

	if s.Mirror != nil {
		s.MirrorSave(ctx, tr)
	}

	s.recordUpdated(ctx, tr.ID, agentID, changes)
	if s.Hub != nil {
		s.Hub.Publish(ctx, ws.Event{
			Topic: "tasks",
			Body: map[string]any{
				"type":  "task.updated",
				"task":  tr,
				"actor": agentID,
			},
		})
	}

	return &EditProposalDiff{Before: &before, After: tr, Changes: changes}, nil
}

// RetractProposal hard-deletes the agent's un-triaged proposal.
//
// Same gate as EditProposal. Hard delete (not soft) — the convention
// from the AGENTS.md "soft delete only on projects" rule. We audit
// task.deleted with a snapshot of the task in the payload so the
// timeline still shows "agent X proposed-and-then-retracted Y".
func (s *Service) RetractProposal(ctx context.Context, taskID, agentID string) error {
	tr, err := s.Tasks.GetByID(ctx, taskID)
	if err != nil {
		return err
	}
	if !IsOwnProposal(tr, agentID) {
		return ErrNotOwnProposal
	}

	// Phase 33.2.1: write the tombstone BEFORE the delete so a
	// concurrent owner triage that already moved the row past
	// backlog leaves us with no orphan audit. DeleteWithProposalGate
	// asserts the same gate as UpdateProposalFields (created_by_type
	// 'agent', created_by_id=me, status='backlog'); RowsAffected()==0
	// → ErrConcurrentTriage. The tombstone write runs against the
	// pre-delete row captured here; the AND-gate's matching is
	// authoritative on whether the retract is allowed.
	snapJSON := fmt.Sprintf(`{"id":%q,"title":%q,"project_id":%q}`,
		tr.ID, tr.Title, tr.ProjectID)
	if s.Tombstone != nil {
		if err := s.Tombstone.RecordRetracted(ctx, taskID,
			snapJSON, activity.ActorAgent, agentID); err != nil {
			zap.L().Warn("manage: tombstone record failed (pre-delete)",
				zap.String("task_id", taskID),
				zap.Error(err))
		}
	} else if s.Recorder != nil {
		// Legacy path: best-effort activity row — likely to vanish
		// post-delete via FK CASCADE.
		if err := s.Recorder.Record(ctx, taskID,
			activity.ActorAgent, agentID,
			activity.ActionDeleted, snapJSON); err != nil {
			zap.L().Warn("manage: activity record failed",
				zap.String("task_id", taskID),
				zap.String("action", string(activity.ActionDeleted)),
				zap.Error(err))
		}
	}

	if err := s.Tasks.DeleteWithProposalGate(ctx, taskID, agentID); err != nil {
		if errors.Is(err, task.ErrNotFound) {
			return ErrConcurrentTriage
		}
		return fmt.Errorf("task.RetractProposal: delete: %w", err)
	}

	if s.Mirror != nil {
		_ = s.Mirror.DeleteTask(taskID)
	}

	if s.Hub != nil {
		s.Hub.Publish(ctx, ws.Event{
			Topic: "tasks",
			Body: map[string]any{
				"type":  "task.deleted",
				"task":  tr,
				"actor": agentID,
			},
		})
	}
	return nil
}

// UpdateAgentNotes is the narrow seam the holder uses to write
// agent_notes. The service rejects every other field write through
// this path — the wiki explicitly forbids "holder touches non-notes
// fields on a triaged task" (channel = comments).
//
// The PATCH handler in the api package routes PATCHes with ONLY
// agent_notes (or {"agent_notes": "..."} alongside a holder's
// lock) through this method; PATCHes with any other field on a
// non-own row fail with ErrNotOwnProposal (EditProposal).
func (s *Service) UpdateAgentNotes(ctx context.Context, taskID, agentID, notes string) (*task.Task, error) {
	// Phase 33.2.1: gate-protected partial update. Pre-write, read
	// the row only to (a) confirm the caller is the holder (we
	// still trust the holder check here, defense in depth) and
	// (b) capture the before-snapshot for the activity row. The
	// WRITE doesn't pull a fresh task snapshot — a concurrent
	// Release that clears the assignee leaves RowsAffected()==0.
	tr, err := s.Tasks.GetByID(ctx, taskID)
	if err != nil {
		return nil, err
	}
	if !s.HolderAgentNotesOnly(ctx, tr, agentID) {
		return nil, ErrNotLockHolder
	}
	before := tr.AgentNotes
	if before == notes {
		return tr, nil
	}
	if err := s.Tasks.UpdateAgentNotesField(ctx, taskID, agentID, notes); err != nil {
		if errors.Is(err, task.ErrNotFound) {
			return nil, ErrConcurrentTriage
		}
		return nil, fmt.Errorf("task.UpdateAgentNotes: update: %w", err)
	}
	// Re-read for the canonical after-state (the WS payload /
	// mirror). Cheap PK lookup.
	updated, err := s.Tasks.GetByID(ctx, taskID)
	if err != nil {
		return nil, fmt.Errorf("task.UpdateAgentNotes: re-read: %w", err)
	}
	tr = updated
	if s.Mirror != nil {
		s.MirrorSave(ctx, tr)
	}
	if s.Recorder != nil {
		if err := s.Recorder.Record(ctx, tr.ID,
			activity.ActorAgent, agentID,
			activity.ActionAgentNotes,
			fmt.Sprintf(`{"from":%q,"to":%q}`, before, notes)); err != nil {
			zap.L().Warn("manage: activity record failed",
				zap.String("task_id", tr.ID),
				zap.String("action", string(activity.ActionAgentNotes)),
				zap.Error(err))
		}
	}
	if s.Hub != nil {
		s.Hub.Publish(ctx, ws.Event{
			Topic: "tasks",
			Body: map[string]any{
				"type":  "task.updated",
				"task":  tr,
				"actor": agentID,
			},
		})
	}
	return tr, nil
}

// ----------------------------------------------------------------------------
// patch application
// ----------------------------------------------------------------------------

// applyEditProposalPatch mutates tr based on patch. Returns the list
// of changes for the audit row. The mutation is in-place on tr —
// callers must copy `tr` first if they want a "before" snapshot.
func applyEditProposalPatch(tr *task.Task, patch EditProposalPatch) []Change {
	var changes []Change
	if patch.Title != nil && *patch.Title != tr.Title {
		changes = append(changes, Change{Field: "title", From: tr.Title, To: *patch.Title})
		tr.Title = *patch.Title
	}
	if patch.Description != nil && *patch.Description != tr.Description {
		changes = append(changes, Change{Field: "description", From: tr.Description, To: *patch.Description})
		tr.Description = *patch.Description
	}
	if patch.Priority != nil && *patch.Priority != tr.Priority {
		changes = append(changes, Change{Field: "priority", From: string(tr.Priority), To: string(*patch.Priority)})
		tr.Priority = *patch.Priority
	}
	// due_at: pointer-to-time distinguishes "leave alone" (nil)
	// from "explicitly clear" (&time.Time{} zero value). The wire
	// shape encodes both — the API parses "" as nil and RFC3339 as
	// a real pointer; for the service layer we keep the same
	// contract (nil = no change, &zero = clear).
	if patch.DueAt != nil {
		fromStr := ""
		if tr.DueAt != nil {
			fromStr = tr.DueAt.Format(time.RFC3339)
		}
		toStr := ""
		if !patch.DueAt.IsZero() {
			toStr = patch.DueAt.Format(time.RFC3339)
		}
		if fromStr != toStr {
			changes = append(changes, Change{Field: "due_at", From: fromStr, To: toStr})
			if patch.DueAt.IsZero() {
				tr.DueAt = nil
			} else {
				t := *patch.DueAt
				tr.DueAt = &t
			}
		}
	}
	if patch.ParentTaskID != nil && *patch.ParentTaskID != tr.ParentTaskID {
		changes = append(changes, Change{Field: "parent_task_id", From: tr.ParentTaskID, To: *patch.ParentTaskID})
		tr.ParentTaskID = *patch.ParentTaskID
	}
	return changes
}

// isEmpty reports whether the patch carries no actionable fields. The
// handler treats this as a 400 to make caller bugs visible (an empty
// PATCH is more likely a bug than an intentional no-op).
func (p EditProposalPatch) isEmpty() bool {
	return p.Title == nil && p.Description == nil && p.Priority == nil &&
		p.DueAt == nil && p.ParentTaskID == nil && p.BlockedBy == nil
}

// recordUpdated writes a task.updated activity row summarising the
// per-field changes. The payload is JSON-of-changes — the existing
// TaskViewBody verb map reads both `task.updated` and `task.*` verbs
// from a single place, and a single field-diff row keeps the
// timeline compact when an agent batches several edits in one PATCH.
func (s *Service) recordUpdated(ctx context.Context, taskID, agentID string, changes []Change) {
	if s.Recorder == nil || len(changes) == 0 {
		return
	}
	buf := []byte(`{"changes":[`)
	for i, ch := range changes {
		if i > 0 {
			buf = append(buf, ',')
		}
		buf = append(buf, fmt.Sprintf(`{"field":%q,"from":%q,"to":%q}`, ch.Field, ch.From, ch.To)...)
	}
	buf = append(buf, ']', '}')
	if err := s.Recorder.Record(ctx, taskID,
		activity.ActorAgent, agentID,
		activity.ActionUpdated, string(buf)); err != nil {
		zap.L().Warn("manage: activity record failed",
			zap.String("task_id", taskID),
			zap.String("action", string(activity.ActionUpdated)),
			zap.Error(err))
	}
}
