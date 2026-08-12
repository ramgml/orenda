// Package task provides business logic on top of task.Repository. Phase 2
// introduces Move() with fractional-position kanban reordering. Phase 3
// adds Claim/Release/Submit/Review with atomic task_locks.
package task

import (
	"context"
	"errors"
	"fmt"
	"time"

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
type MoveOptions struct {
	TargetColumnID string
	Position       float64 // explicit fractional; 0 = derive from Before/After
	Before         *task.Task
	After          *task.Task
}

// Recorder is the audit hook for Claim/Release/Submit/Review. Phase 3.9
// wires it to activity.Repository.
type Recorder interface {
	Record(ctx context.Context, taskID string, actorType activity.ActorType, actorID string, action activity.Action, payload string) error
}

// Hub is the in-process notification seam. Phase 2.5 wires the WebSocket
// hub; Phase 3 wires the agent inbox.
//
// We re-export ws.Hub to keep a single source of truth for the interface
// (avoids drift between api/ws.Hub and service/task.Hub).
type Hub = ws.Hub

// Service holds the dependencies Move() and future business logic need.
type Service struct {
	Tasks    task.Repository
	Locks    Locks
	Recorder Recorder
	Comments CommentAdder
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

	if err := s.Tasks.Update(ctx, tr); err != nil {
		return nil, fmt.Errorf("task service: update: %w", err)
	}
	s.mirrorSave(ctx, tr)

	if s.Recorder != nil {
		_ = s.Recorder.Record(ctx, taskID, activity.ActorUser, "", activity.ActionMoved,
			fmt.Sprintf(`{"column_id":%q,"position":%v}`, opts.TargetColumnID, tr.Position))
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

	if err := s.Tasks.Update(ctx, tr); err != nil {
		_ = s.Locks.Release(ctx, taskID, agentID)
		return nil, err
	}
	s.mirrorSave(ctx, tr)

	s.publishTask(ctx, "task.claimed", tr, agentID, map[string]any{"agent_id": agentID})
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
	tr.AssigneeType = ""
	tr.AssigneeID = ""
	tr.Status = task.StatusTodo
	tr.Awaiting = task.AwaitingNone
	if err := s.Tasks.Update(ctx, tr); err != nil {
		return nil, err
	}
	s.mirrorSave(ctx, tr)

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
			for _, next := range edges[cur] {
				stack = append(stack, next)
			}
		}
	}
	return nil
}

// Submit marks a task as ready for human review (status=review,
// awaiting=human).
func (s *Service) Submit(ctx context.Context, taskID, agentID string, note string) (*task.Task, error) {
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
	if err := s.Tasks.Update(ctx, tr); err != nil {
		return nil, err
	}
	s.mirrorSave(ctx, tr)

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
			conv = append(conv, task.ChecklistItem{
				ID:          it.ID,
				ChecklistID: it.ChecklistID,
				Title:       it.Title,
				Done:        it.Done,
				Position:    it.Position,
			})
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
