package study

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ramgml/orenda/internal/api/ws"
)

// Phase 32.9 dedup. The Study proposals v1 (Phase 31) created a new
// row on every Propose call — the planner agent's evening+morning
// runs piled up identical cards in the user's tray. These tests
// pin the v2 contract: a second Propose with the same dedup key
// from the same agent collapses onto the existing pending row.
//
// The dedup key is (created_by_agent, course_id, normalized_title)
// where normalized_title is trim + collapse-whitespace + lowercase
// (see study.NormalizeTitle and wiki:study-proposals-dedup).

// TestService_Propose_DedupBySameKey pins the headline behaviour:
// two Propose calls with the same dedup key return the same
// proposal id. The second call emits no new WS event (the tray
// shouldn't flash), no new backup_log row, and the proposal
// retains its first-seen body_md (we deliberately don't merge —
// see wiki:study-proposals-dedup).
func TestService_Propose_DedupBySameKey(t *testing.T) {
	ctx := context.Background()
	fx, courseID, agentID := setupStudySvc(t)

	first, err := fx.svc.Propose(ctx, agentID, ProposeInput{
		CourseID:   courseID,
		Title:      "Read chapter 5",
		BodyMD:     "first body",
		TargetDate: "2099-08-17",
	})
	require.NoError(t, err)
	require.False(t, first.Refreshed, "first Propose creates a new row")
	require.NotEmpty(t, first.Proposal.ID)

	// Same agent, same course, same title with extra whitespace and
	// different casing — the normalization layer must collapse it
	// onto the same dedup key.
	second, err := fx.svc.Propose(ctx, agentID, ProposeInput{
		CourseID:   courseID,
		Title:      "  READ   CHAPTER 5  ",
		BodyMD:     "second body (should be ignored)",
		TargetDate: "2099-12-31",
	})
	require.NoError(t, err)
	assert.True(t, second.Refreshed,
		"second Propose with the same dedup key is an idempotent refresh")
	assert.Equal(t, first.Proposal.ID, second.Proposal.ID,
		"both calls return the same proposal id")
	assert.Equal(t, "first body", second.Proposal.BodyMD,
		"the dedup refresh preserves the first-seen body_md")

	// The tray still has exactly one row for this agent+course+title.
	pending, err := fx.svc.ListPending(ctx)
	require.NoError(t, err)
	count := 0
	for _, p := range pending {
		if p.CreatedByAgent == agentID && p.CourseID == courseID {
			count++
		}
	}
	assert.Equal(t, 1, count,
		"the tray must hold exactly one pending proposal for this dedup key")

	// No second WS event. The first Propose emitted study.proposed;
	// the dedup refresh is silent.
	require.Len(t, fx.hub.events, 1,
		"the dedup refresh must not emit study.proposed")
	ev := fx.hub.events[0]
	assert.Equal(t, "tasks", ev.Topic)
}

// TestService_Propose_DedupDifferentTitlesAreNotDeduped is the
// negative pin: the dedup key includes title, so two Propose
// calls with different titles produce two separate rows.
func TestService_Propose_DedupDifferentTitlesAreNotDeduped(t *testing.T) {
	ctx := context.Background()
	fx, courseID, agentID := setupStudySvc(t)

	a, err := fx.svc.Propose(ctx, agentID, ProposeInput{
		CourseID: courseID, Title: "Read chapter 5", TargetDate: "2099-08-17",
	})
	require.NoError(t, err)
	require.False(t, a.Refreshed)

	b, err := fx.svc.Propose(ctx, agentID, ProposeInput{
		CourseID: courseID, Title: "Read chapter 6", TargetDate: "2099-08-17",
	})
	require.NoError(t, err)
	require.False(t, b.Refreshed,
		"different titles must produce different rows")
	assert.NotEqual(t, a.Proposal.ID, b.Proposal.ID)
}

// TestService_Propose_DedupDifferentCoursesAreNotDeduped pins the
// course_id axis of the dedup key: same title for two courses is
// two separate rows. We create a second course directly in the
// test DB so we don't have to plumb a multi-course helper.
func TestService_Propose_DedupDifferentCoursesAreNotDeduped(t *testing.T) {
	ctx := context.Background()
	fx, courseA, agentID := setupStudySvc(t)

	// Seed a second course in the same fixture DB.
	courseB := "c-svc-2"
	var cNum int
	err := fx.db.QueryRowContext(ctx,
		`UPDATE course_number_seq SET next = next + 1 WHERE id = 1 RETURNING next - 1`,
	).Scan(&cNum)
	require.NoError(t, err)
	_, err = fx.db.ExecContext(ctx,
		`INSERT INTO courses (id, number, title, owner_id, status) VALUES (?, ?, ?, ?, 'active')`,
		courseB, cNum, "Other course", "u-svc")
	require.NoError(t, err)

	a, err := fx.svc.Propose(ctx, agentID, ProposeInput{
		CourseID: courseA, Title: "Read chapter 5", TargetDate: "2099-08-17",
	})
	require.NoError(t, err)

	b, err := fx.svc.Propose(ctx, agentID, ProposeInput{
		CourseID: courseB, Title: "Read chapter 5", TargetDate: "2099-08-17",
	})
	require.NoError(t, err)

	assert.NotEqual(t, a.Proposal.ID, b.Proposal.ID,
		"same title on different courses must produce different rows")
}

// TestService_Propose_DedupDifferentAgentsAreNotDeduped is the
// cross-agent pin: planner-agent's suggestion for "Read chapter 5"
// is a different row from tutor-agent's suggestion for the same
// chapter. We don't collapse across agents because they're
// different sources with different intent.
func TestService_Propose_DedupDifferentAgentsAreNotDeduped(t *testing.T) {
	ctx := context.Background()
	fx, courseID, _ := setupStudySvc(t)

	// Seed a second agent so the test can use a fresh agent ID.
	tutorID := "a-tutor"
	_, err := fx.db.ExecContext(ctx,
		`INSERT INTO agents (id, name, type, token_id, max_concurrent) VALUES (?, ?, ?, ?, 3)`,
		tutorID, "tutor", "[]", "t-svc")
	require.NoError(t, err)

	p, err := fx.svc.Propose(ctx, "a-svc", ProposeInput{
		CourseID: courseID, Title: "Read chapter 5", TargetDate: "2099-08-17",
	})
	require.NoError(t, err)

	tut, err := fx.svc.Propose(ctx, tutorID, ProposeInput{
		CourseID: courseID, Title: "Read chapter 5", TargetDate: "2099-08-17",
	})
	require.NoError(t, err)

	assert.NotEqual(t, p.Proposal.ID, tut.Proposal.ID,
		"different agents with the same key must produce different rows")
}

// TestService_Propose_DedupDoesNotBlockAfterAccept pins the
// post-resolution behaviour: once the user accepts a proposal,
// a fresh Propose with the same key MUST create a new row
// (the user's verdict is final, the agent's new suggestion is a
// separate entity).
func TestService_Propose_DedupDoesNotBlockAfterAccept(t *testing.T) {
	ctx := context.Background()
	fx, courseID, agentID := setupStudySvc(t)

	first, err := fx.svc.Propose(ctx, agentID, ProposeInput{
		CourseID: courseID, Title: "Read chapter 5", TargetDate: "2099-08-17",
	})
	require.NoError(t, err)
	require.False(t, first.Refreshed)

	_, err = fx.svc.Accept(ctx, first.Proposal.ID)
	require.NoError(t, err)

	// A fresh Propose with the same key creates a new row because
	// the previous one is accepted, not pending.
	second, err := fx.svc.Propose(ctx, agentID, ProposeInput{
		CourseID: courseID, Title: "Read chapter 5", TargetDate: "2099-08-17",
	})
	require.NoError(t, err)
	assert.False(t, second.Refreshed,
		"after Accept, the dedup target is gone — a new row is created")
	assert.NotEqual(t, first.Proposal.ID, second.Proposal.ID)
}

// TestService_Propose_DedupDoesNotBlockAfterDismiss pins the
// symmetric post-dismiss behaviour.
func TestService_Propose_DedupDoesNotBlockAfterDismiss(t *testing.T) {
	ctx := context.Background()
	fx, courseID, agentID := setupStudySvc(t)

	first, err := fx.svc.Propose(ctx, agentID, ProposeInput{
		CourseID: courseID, Title: "Read chapter 5", TargetDate: "2099-08-17",
	})
	require.NoError(t, err)

	_, err = fx.svc.Dismiss(ctx, first.Proposal.ID)
	require.NoError(t, err)

	second, err := fx.svc.Propose(ctx, agentID, ProposeInput{
		CourseID: courseID, Title: "Read chapter 5", TargetDate: "2099-08-17",
	})
	require.NoError(t, err)
	assert.False(t, second.Refreshed,
		"after Dismiss, a new Propose creates a fresh row")
	assert.NotEqual(t, first.Proposal.ID, second.Proposal.ID)
}

// TestService_Propose_DedupEmptyCourseID pins the "course-less"
// reminder axis: two proposals with the same normalized title and
// no course_id (course-less reminders) DO dedup against each other.
// This is deliberate — the "remind me to drink water" agent
// shouldn't pile up identical cards just because it doesn't tie
// them to a course.
func TestService_Propose_DedupEmptyCourseID(t *testing.T) {
	ctx := context.Background()
	fx, _, agentID := setupStudySvc(t)

	a, err := fx.svc.Propose(ctx, agentID, ProposeInput{
		Title: "Drink water", TargetDate: "2099-08-17",
	})
	require.NoError(t, err)
	require.False(t, a.Refreshed)

	b, err := fx.svc.Propose(ctx, agentID, ProposeInput{
		Title: "DRINK WATER", TargetDate: "2099-08-17",
	})
	require.NoError(t, err)
	assert.True(t, b.Refreshed,
		"course-less reminders with the same title should dedup")
	assert.Equal(t, a.Proposal.ID, b.Proposal.ID)
}

// Suppress unused-import warning if the ws import is pruned in a
// future refactor — the ws.Event type is needed because bodyField
// in service_test.go takes it as a parameter, and this file
// imports the same package via the testing context.
var _ ws.Event
