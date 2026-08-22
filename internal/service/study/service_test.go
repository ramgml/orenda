package study

import (
	"context"
	"database/sql"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ramgml/orenda/internal/api/ws"
	"github.com/ramgml/orenda/internal/domain/activity"
	"github.com/ramgml/orenda/internal/domain/study"
	"github.com/ramgml/orenda/internal/storage/sqlite"
)

// studySvcFixture wires the service against the real SQLite repos
// (same package) plus a recording Hub and a recording ActivityRecorder.
type studySvcFixture struct {
	svc      *Service
	hub      *recordingHub
	recorder *recordingRecorder
	db       *sql.DB
}

func setupStudySvc(t *testing.T) (*studySvcFixture, string, string) {
	t.Helper()
	ctx := context.Background()

	dir := t.TempDir()
	dbPath := dir + "/orenda.db"
	db, err := sqlite.Open(ctx, dbPath, sqlite.OpenConfig{
		WALMode: true, EnableForeign: true, BusyTimeoutMs: 5000,
	})
	require.NoError(t, err)
	require.NoError(t, sqlite.Migrate(ctx, db, sqlite.MigrationsFS, "migrations"))
	t.Cleanup(func() { _ = db.Close() })

	propRepo := sqlite.NewStudyProposalRepository(db)
	taskRepo := sqlite.NewTaskRepository(db)

	hub := &recordingHub{}
	rec := &recordingRecorder{}
	svc := New(propRepo, taskRepo, hub, rec)

	const (
		ownerID  = "u-svc"
		courseID = "c-svc"
		agentID  = "a-svc"
	)
	_, err = db.ExecContext(ctx,
		`INSERT INTO users (id, email, password_hash, display_name) VALUES (?, ?, ?, ?)`,
		ownerID, "svc@031.local", "x", "U")
	require.NoError(t, err)
	_, err = db.ExecContext(ctx,
		`INSERT INTO api_tokens (id, user_id, name, hash, scopes) VALUES (?, ?, ?, ?, '[]')`,
		"t-svc", ownerID, "seed", "h")
	require.NoError(t, err)
	_, err = db.ExecContext(ctx,
		`INSERT INTO agents (id, name, type, token_id, max_concurrent) VALUES (?, ?, ?, ?, 3)`,
		agentID, "planner", "[]", "t-svc")
	require.NoError(t, err)
	var cNum int
	err = db.QueryRowContext(ctx,
		`UPDATE course_number_seq SET next = next + 1 WHERE id = 1 RETURNING next - 1`,
	).Scan(&cNum)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx,
		`INSERT INTO courses (id, number, title, owner_id, status) VALUES (?, ?, ?, ?, 'active')`,
		courseID, cNum, "Rust", ownerID)
	require.NoError(t, err)

	return &studySvcFixture{svc: svc, hub: hub, recorder: rec, db: db}, courseID, agentID
}

// bodyField fetches a string field from the event body. We use
// the body map for everything — the service emits a flat
// {proposal_id, course_id?, task_id?, agent_id?} alongside the
// embedded proposal.
func bodyField(t *testing.T, ev ws.Event, key string) string {
	t.Helper()
	m, ok := ev.Body.(map[string]any)
	require.True(t, ok, "event body must be a map (got %T)", ev.Body)
	v, ok := m[key]
	if !ok {
		return ""
	}
	s, _ := v.(string)
	return s
}

// proposalFromEvent pulls the embedded Proposal out of the event
// body so tests can assert its full state. The service marshals the
// proposal into a map[string]any inside the body, so we JSON-
// roundtrip through encoding/json to restore the typed value.
func proposalFromEvent(t *testing.T, ev ws.Event) *study.Proposal {
	t.Helper()
	raw, ok := ev.Body.(map[string]any)["proposal"]
	require.True(t, ok, "event must embed the proposal under body.proposal")
	buf, err := json.Marshal(raw)
	require.NoError(t, err)
	var p study.Proposal
	require.NoError(t, json.Unmarshal(buf, &p))
	return &p
}

// TestService_Propose_PersistsAndEmits pins the propose path:
// Create lands a pending proposal; the hub receives a
// study.proposed event with proposal_id + agent_id + course_id.
func TestService_Propose_PersistsAndEmits(t *testing.T) {
	ctx := context.Background()
	fx, courseID, agentID := setupStudySvc(t)

	in := ProposeInput{
		CourseID:   courseID,
		Title:      "Read chapter 5",
		BodyMD:     "rust-book chapter 5",
		TargetDate: "2026-08-17",
	}
	res, err := fx.svc.Propose(ctx, agentID, in)
	require.NoError(t, err)
	p := res.Proposal
	assert.Equal(t, study.StatusPending, p.Status)
	assert.Equal(t, agentID, p.CreatedByAgent, "service stamps agent_id")
	assert.NotEmpty(t, p.ID, "repo mints id")
	assert.False(t, p.CreatedAt.IsZero())

	// Hub saw the event.
	require.Len(t, fx.hub.events, 1)
	ev := fx.hub.events[0]
	assert.Equal(t, "tasks", ev.Topic)
	assert.Equal(t, p.ID, bodyField(t, ev, "proposal_id"))
	assert.Equal(t, courseID, bodyField(t, ev, "course_id"))
	assert.Equal(t, agentID, bodyField(t, ev, "agent_id"))
	// Embedded proposal mirrors what the repo persisted.
	embedded := proposalFromEvent(t, ev)
	assert.Equal(t, p.ID, embedded.ID)
	assert.Equal(t, study.StatusPending, embedded.Status)
}

// TestService_Accept_HappyPath: accept materialises an inbox task
// with study_course_id and due_at = target_date's end-of-day.
func TestService_Accept_HappyPath(t *testing.T) {
	ctx := context.Background()
	fx, courseID, agentID := setupStudySvc(t)

	res, err := fx.svc.Propose(ctx, agentID, ProposeInput{
		CourseID: courseID, Title: "Read chapter 5", TargetDate: "2099-08-17",
	})
	require.NoError(t, err)
	p := res.Proposal

	acceptRes, err := fx.svc.Accept(ctx, p.ID)
	require.NoError(t, err)
	_ = acceptRes
	require.NoError(t, err)
	assert.False(t, acceptRes.AlreadyAccepted)
	assert.Equal(t, study.StatusAccepted, acceptRes.Proposal.Status)
	assert.NotNil(t, acceptRes.Task)
	assert.Equal(t, courseID, acceptRes.Task.StudyCourseID)
	assert.Equal(t, "Read chapter 5", acceptRes.Task.Title)
	require.NotNil(t, acceptRes.Task.DueAt, "due_at populated from target_date")
	// Target is 2099 — far future — so due_at lands on the
	// target's end-of-day, not today.
	assert.Equal(t, 2099, acceptRes.Task.DueAt.Year())
	assert.Equal(t, time.August, acceptRes.Task.DueAt.Month())

	// Activity row written.
	require.Len(t, fx.recorder.records, 1)
	r := fx.recorder.records[0]
	assert.Equal(t, acceptRes.Task.ID, r.TaskID)
	assert.Equal(t, activity.ActionCreated, r.Action)
	assert.Equal(t, activity.ActorAgent, r.ActorType)
	assert.Equal(t, agentID, r.ActorID)

	// Hub saw study.accepted.
	require.Len(t, fx.hub.events, 2)
	ev := fx.hub.events[1]
	assert.Equal(t, acceptRes.Task.ID, bodyField(t, ev, "task_id"))
}

// TestService_Accept_Idempotent: re-running Accept on an
// already-accepted proposal returns the same task and the
// AlreadyAccepted flag is set.
func TestService_Accept_Idempotent(t *testing.T) {
	ctx := context.Background()
	fx, courseID, agentID := setupStudySvc(t)

	res, err := fx.svc.Propose(ctx, agentID, ProposeInput{
		CourseID: courseID, Title: "Idempotent", TargetDate: "2099-08-17",
	})
	require.NoError(t, err)
	p := res.Proposal

	first, err := fx.svc.Accept(ctx, p.ID)
	require.NoError(t, err)
	require.False(t, first.AlreadyAccepted)

	second, err := fx.svc.Accept(ctx, p.ID)
	require.NoError(t, err)
	assert.True(t, second.AlreadyAccepted, "second call surfaces idempotent flag")
	assert.Equal(t, first.Task.ID, second.Task.ID, "returns the original task id")

	// No second task row created.
	var count int
	require.NoError(t, fx.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM tasks WHERE study_course_id = ?`, courseID).Scan(&count))
	assert.Equal(t, 1, count, "exactly one inbox task created, even after a re-accept")

	// Activity written once, not twice.
	assert.Len(t, fx.recorder.records, 1, "audit log records the first accept only")
}

// TestService_Accept_TargetDateBeforeToday_UsesToday pins the
// "max(target_date, today)" policy: a proposal for 2024 accepted
// in 2026 lands with due_at = today, not 2024.
func TestService_Accept_TargetDateBeforeToday_UsesToday(t *testing.T) {
	ctx := context.Background()
	fx, courseID, agentID := setupStudySvc(t)

	res, err := fx.svc.Propose(ctx, agentID, ProposeInput{
		CourseID: courseID, Title: "Old proposal", TargetDate: "2024-01-01",
	})
	require.NoError(t, err)
	p := res.Proposal

	acceptRes, err := fx.svc.Accept(ctx, p.ID)
	require.NoError(t, err)
	_ = acceptRes
	require.NoError(t, err)
	require.NotNil(t, acceptRes.Task.DueAt)

	now := time.Now().UTC()
	due := *acceptRes.Task.DueAt
	assert.True(t, due.Year() >= now.Year(),
		"proposal for 2024 accepted in 2026 lands with due_at >= today (got %v)", due)
}

// TestService_Dismiss_HappyPath: dismiss flips status, no task
// materialises, hub sees study.dismissed.
func TestService_Dismiss_HappyPath(t *testing.T) {
	ctx := context.Background()
	fx, courseID, agentID := setupStudySvc(t)

	res, err := fx.svc.Propose(ctx, agentID, ProposeInput{
		CourseID: courseID, Title: "Skip this", TargetDate: "2099-08-17",
	})
	require.NoError(t, err)
	p := res.Proposal

	got, err := fx.svc.Dismiss(ctx, p.ID)
	require.NoError(t, err)
	assert.Equal(t, study.StatusDismissed, got.Status)

	// No task created.
	var count int
	require.NoError(t, fx.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM tasks WHERE study_course_id = ?`, courseID).Scan(&count))
	assert.Equal(t, 0, count, "dismiss does not create a task")

	// No activity written.
	assert.Empty(t, fx.recorder.records, "dismiss does not emit a task.created row")

	// Hub saw study.dismissed.
	require.Len(t, fx.hub.events, 2) // proposed + dismissed
	assert.NotEmpty(t, bodyField(t, fx.hub.events[1], "proposal_id"))
}

// TestService_Accept_OnDismissed_ReturnsTransition pins the
// lifecycle guard: accept on a dismissed proposal is ErrTransition,
// not idempotent.
func TestService_Accept_OnDismissed_ReturnsTransition(t *testing.T) {
	ctx := context.Background()
	fx, courseID, agentID := setupStudySvc(t)

	res, err := fx.svc.Propose(ctx, agentID, ProposeInput{
		CourseID: courseID, Title: "Don't double-dip", TargetDate: "2099-08-17",
	})
	require.NoError(t, err)
	p := res.Proposal
	_, err = fx.svc.Dismiss(ctx, p.ID)
	require.NoError(t, err)

	_, err = fx.svc.Accept(ctx, p.ID)
	require.ErrorIs(t, err, study.ErrTransition)
}

// TestService_Accept_OnAlreadyAccepted_IsIdempotent is the
// opposite of the above — accept on accepted is allowed and
// returns the existing task.
func TestService_Accept_OnAlreadyAccepted_IsIdempotent(t *testing.T) {
	ctx := context.Background()
	fx, courseID, agentID := setupStudySvc(t)

	res, err := fx.svc.Propose(ctx, agentID, ProposeInput{
		CourseID: courseID, Title: "Repeat", TargetDate: "2099-08-17",
	})
	require.NoError(t, err)
	p := res.Proposal
	_, err = fx.svc.Accept(ctx, p.ID)
	require.NoError(t, err)

	acceptRes, err := fx.svc.Accept(ctx, p.ID)
	require.NoError(t, err)
	_ = acceptRes
	require.NoError(t, err)
	assert.True(t, acceptRes.AlreadyAccepted)
}

// TestService_Propose_InvalidInput — the service forwards Validate
// errors from the repo. Empty title fails (Phase 31.2 contract).
func TestService_Propose_InvalidInput(t *testing.T) {
	ctx := context.Background()
	fx, _, agentID := setupStudySvc(t)

	_, err := fx.svc.Propose(ctx, agentID, ProposeInput{
		Title: "", TargetDate: "2026-08-17",
	})
	require.ErrorIs(t, err, study.ErrInvalidInput)
}

// TestService_ListPending_HappyPath — ordering and filtering.
func TestService_ListPending_HappyPath(t *testing.T) {
	ctx := context.Background()
	fx, courseID, agentID := setupStudySvc(t)

	for _, title := range []string{"A", "B", "C"} {
		_, err := fx.svc.Propose(ctx, agentID, ProposeInput{
			CourseID:   courseID,
			Title:      title,
			TargetDate: "2099-08-17",
		})
		require.NoError(t, err)
	}

	// Dismiss one — list should now return 2.
	all, err := fx.svc.ListPending(ctx)
	require.NoError(t, err)
	require.Len(t, all, 3)

	_, err = fx.svc.Dismiss(ctx, all[1].ID)
	require.NoError(t, err)

	pending, err := fx.svc.ListPending(ctx)
	require.NoError(t, err)
	assert.Len(t, pending, 2, "dismissed proposal leaves the pending list")
	for _, p := range pending {
		assert.NotEqual(t, all[1].ID, p.ID, "dismissed id must not appear")
	}
}

// TestService_Accept_UnknownProposal — missing proposal surfaces
// ErrNotFound (404 on the API).
func TestService_Accept_UnknownProposal(t *testing.T) {
	ctx := context.Background()
	fx, _, _ := setupStudySvc(t)
	_, err := fx.svc.Accept(ctx, "no-such-proposal")
	require.ErrorIs(t, err, study.ErrNotFound)
}

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

// recordingHub captures every Publish so tests can assert the wire
// shape without standing up a real websocket.
type recordingHub struct {
	mu     sync.Mutex
	events []ws.Event
}

func (h *recordingHub) Publish(_ context.Context, e ws.Event) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.events = append(h.events, e)
}

func (h *recordingHub) Close() {}

func (h *recordingHub) Subscribe(string, string) (<-chan ws.Event, ws.Unsubscribe) {
	ch := make(chan ws.Event)
	close(ch)
	return ch, func() {}
}

// recordingRecorder captures the activity rows the service emits.
type recorderRecord struct {
	TaskID    string
	ActorType activity.ActorType
	ActorID   string
	Action    activity.Action
	Payload   string
}

type recordingRecorder struct {
	mu      sync.Mutex
	records []recorderRecord
}

func (r *recordingRecorder) RecordTask(_ context.Context, taskID string, actorType activity.ActorType, actorID string, action activity.Action, payload string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.records = append(r.records, recorderRecord{
		TaskID: taskID, ActorType: actorType, ActorID: actorID,
		Action: action, Payload: payload,
	})
	return nil
}
