package task_test

// master-agent-role plan (step 3): user-namespace write paths take the
// actor from the identity — a master agent's Move/Review/RecordActivity
// must land in task_activity (and the review comment) as agent-authored,
// while the empty actorType keeps the historical user default.

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ramgml/orenda/internal/domain/activity"
	"github.com/ramgml/orenda/internal/domain/task"
	taskservice "github.com/ramgml/orenda/internal/service/task"
)

// captureComments is a CommentAdder fake that remembers the authorship
// of every added comment.
type captureComments struct {
	authorTypes []string
	authorIDs   []string
}

func (c *captureComments) Add(_ context.Context, in *taskservice.CommentInput) (string, error) {
	c.authorTypes = append(c.authorTypes, in.AuthorType)
	c.authorIDs = append(c.authorIDs, in.AuthorID)
	return "cmt-1", nil
}

func TestService_Move_ActorAttribution(t *testing.T) {
	f := setupPhase278Project(t)
	backlog := columnByStatus(t, f, task.StatusBacklog)
	review := columnByStatus(t, f, task.StatusReview)
	svc := newSvc(t, f)
	agentID := seedP278Agent(t, f, "mover")

	tr := &task.Task{ProjectID: f.project.ID, ColumnID: backlog.ID, Title: "drag me"}
	require.NoError(t, f.taskRepo.Create(context.Background(), tr))

	// Master agent drag: the timeline row is agent-authored.
	_, err := svc.Move(context.Background(), tr.ID, taskservice.MoveOptions{
		TargetColumnID: review.ID,
		ActorType:      activity.ActorAgent,
		ActorID:        agentID,
	})
	require.NoError(t, err)
	require.NotEmpty(t, f.rec.actorTypes)
	assert.Equal(t, activity.ActorAgent, f.rec.actorTypes[len(f.rec.actorTypes)-1],
		"master agent move must record actor_type=agent")
	assert.Contains(t, f.rec.calls[len(f.rec.calls)-1], ":"+agentID+":",
		"actor_id carries the agent, not the owner user")

	// Empty actor type (sync / legacy callers): the user default stands.
	tr2 := &task.Task{ProjectID: f.project.ID, ColumnID: backlog.ID, Title: "sync drag"}
	require.NoError(t, f.taskRepo.Create(context.Background(), tr2))
	_, err = svc.Move(context.Background(), tr2.ID, taskservice.MoveOptions{
		TargetColumnID: review.ID,
		ActorID:        "u-owner",
	})
	require.NoError(t, err)
	assert.Equal(t, activity.ActorUser, f.rec.actorTypes[len(f.rec.actorTypes)-1],
		"empty actorType keeps the historical user default")
}

func TestService_Review_ActorAttribution(t *testing.T) {
	f := setupPhase278Project(t)
	review := columnByStatus(t, f, task.StatusReview)
	svc := newSvc(t, f)
	agentID := seedP278Agent(t, f, "reviewer")
	comments := &captureComments{}
	svc.Comments = comments

	tr := &task.Task{ProjectID: f.project.ID, ColumnID: review.ID, Title: "review me", Status: task.StatusReview}
	require.NoError(t, f.taskRepo.Create(context.Background(), tr))

	_, err := svc.Review(context.Background(), tr.ID, activity.ActorAgent, agentID,
		taskservice.ReviewReject, "needs work")
	require.NoError(t, err)

	last := len(f.rec.calls) - 1
	assert.Equal(t, activity.ActorAgent, f.rec.actorTypes[last],
		"master agent review must record actor_type=agent")
	assert.Contains(t, f.rec.calls[last], ":"+agentID+":")
	require.Len(t, comments.authorTypes, 1)
	assert.Equal(t, "agent", comments.authorTypes[0], "rejection comment is agent-authored")
	assert.Equal(t, agentID, comments.authorIDs[0])
}

func TestService_Review_UserDefaultAttribution(t *testing.T) {
	f := setupPhase278Project(t)
	review := columnByStatus(t, f, task.StatusReview)
	svc := newSvc(t, f)
	comments := &captureComments{}
	svc.Comments = comments

	tr := &task.Task{ProjectID: f.project.ID, ColumnID: review.ID, Title: "plain review", Status: task.StatusReview}
	require.NoError(t, f.taskRepo.Create(context.Background(), tr))

	// Empty actorType (legacy callers, e.g. the bot adapter): user.
	_, err := svc.Review(context.Background(), tr.ID, "", "u-owner",
		taskservice.ReviewApprove, "")
	require.NoError(t, err)
	last := len(f.rec.calls) - 1
	assert.Equal(t, activity.ActorUser, f.rec.actorTypes[last])
}

func TestService_RecordActivity_ActorAttribution(t *testing.T) {
	f := setupPhase278Project(t)
	svc := newSvc(t, f)
	agentID := seedP278Agent(t, f, "annotator")

	svc.RecordActivity(context.Background(), "t-attr", activity.ActorAgent, agentID,
		activity.ActionChecklistAdded, `{}`)
	last := len(f.rec.calls) - 1
	require.GreaterOrEqual(t, last, 0)
	assert.Equal(t, activity.ActorAgent, f.rec.actorTypes[last])
	assert.True(t, strings.HasPrefix(f.rec.calls[last], "t-attr:"),
		"activity row is written against the given task, got %q", f.rec.calls[last])

	svc.RecordActivity(context.Background(), "t-attr", "", "u-owner",
		activity.ActionChecklistAdded, `{}`)
	last = len(f.rec.calls) - 1
	assert.Equal(t, activity.ActorUser, f.rec.actorTypes[last],
		"empty actorType keeps the historical user default")
}
