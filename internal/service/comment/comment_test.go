package comment_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ramgml/orenda/internal/api/ws"
	"github.com/ramgml/orenda/internal/domain/comment"
	commentsvc "github.com/ramgml/orenda/internal/service/comment"
	"github.com/ramgml/orenda/internal/storage/sqlite"
	"github.com/ramgml/orenda/internal/testutil"
)

type memHub struct {
	events []ws.Event
}

func (m *memHub) Publish(_ context.Context, e ws.Event) {
	m.events = append(m.events, e)
}

// Close implements ws.Hub (Phase 22.3).
func (m *memHub) Close() {}

func (m *memHub) Subscribe(string, string) (<-chan ws.Event, ws.Unsubscribe) {
	ch := make(chan ws.Event, 1)
	return ch, func() { close(ch) }
}

func setupCommentSvc(t *testing.T) (*commentsvc.Service, *memHub) {
	t.Helper()
	db, _ := testutil.TemplateDBOpen(t)

	hub := &memHub{}
	svc := commentsvc.New(sqlite.NewCommentRepository(db), hub, nil)
	return svc, hub
}

func TestCommentService_Add(t *testing.T) {
	svc, hub := setupCommentSvc(t)

	c := &comment.Comment{
		TargetID: "t-1", AuthorID: "u-1", BodyMD: "hello @user:alice",
	}
	got, err := svc.Add(context.Background(), c)
	require.NoError(t, err)
	assert.NotEmpty(t, got.ID)

	// WS event published.
	require.NotEmpty(t, hub.events)
	body, ok := hub.events[0].Body.(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "comment.added", body["type"])
}

func TestCommentService_Add_InvalidInput(t *testing.T) {
	svc, _ := setupCommentSvc(t)

	_, err := svc.Add(context.Background(), &comment.Comment{TargetID: "", AuthorID: "u", BodyMD: "x"})
	require.Error(t, err)
}

func TestCommentService_Add_NilComment(t *testing.T) {
	svc, _ := setupCommentSvc(t)

	_, err := svc.Add(context.Background(), nil)
	require.Error(t, err)
}

func TestCommentService_ListByTarget(t *testing.T) {
	svc, _ := setupCommentSvc(t)

	for i := 0; i < 3; i++ {
		_, err := svc.Add(context.Background(), &comment.Comment{
			TargetID: "t-1", AuthorID: "u", BodyMD: "msg",
		})
		require.NoError(t, err)
	}
	_, err := svc.Add(context.Background(), &comment.Comment{
		TargetID: "t-2", AuthorID: "u", BodyMD: "other",
	})
	require.NoError(t, err)

	got, err := svc.ListByTarget(context.Background(), comment.TargetTask, "t-1")
	require.NoError(t, err)
	assert.Len(t, got, 3)
}

func TestCommentService_ListByTarget_MissingID(t *testing.T) {
	svc, _ := setupCommentSvc(t)

	_, err := svc.ListByTarget(context.Background(), comment.TargetTask, "")
	require.Error(t, err)
}

func TestCommentService_MentionsForComment(t *testing.T) {
	svc, _ := setupCommentSvc(t)

	got, err := svc.Add(context.Background(), &comment.Comment{
		TargetID: "t", AuthorID: "u", BodyMD: "@user:alice and @agent:qwen",
	})
	require.NoError(t, err)

	mentions, err := svc.MentionsForComment(context.Background(), got.ID)
	require.NoError(t, err)
	assert.Len(t, mentions, 2)
}

func TestCommentService_MentionsForComment_EmptyID(t *testing.T) {
	svc, _ := setupCommentSvc(t)

	_, err := svc.MentionsForComment(context.Background(), "")
	require.Error(t, err)
}

func TestCommentService_Update(t *testing.T) {
	svc, hub := setupCommentSvc(t)
	ctx := context.Background()

	c := &comment.Comment{TargetID: "t-1", AuthorID: "u-1", BodyMD: "v1"}
	created, err := svc.Add(ctx, c)
	require.NoError(t, err)

	got, err := svc.Update(ctx, created.ID, "v2", comment.AuthorUser, "u-1")
	require.NoError(t, err)
	assert.Equal(t, "v2", got.BodyMD)
	require.NotNil(t, got.EditedAt, "edited_at must be stamped on update")

	// WS event published with the new type.
	require.NotEmpty(t, hub.events)
	last := hub.events[len(hub.events)-1]
	body, ok := last.Body.(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "comment.updated", body["type"])
	assert.Equal(t, created.ID, got.ID)
}

func TestCommentService_Update_EmptyBody(t *testing.T) {
	svc, _ := setupCommentSvc(t)

	_, err := svc.Update(context.Background(), "c-1", "   ", comment.AuthorUser, "u-1")
	require.ErrorIs(t, err, commentsvc.ErrInvalidInput)
}

func TestCommentService_Update_WrongAuthor(t *testing.T) {
	svc, _ := setupCommentSvc(t)
	ctx := context.Background()

	c := &comment.Comment{TargetID: "t-1", AuthorID: "u-1", BodyMD: "v1"}
	created, err := svc.Add(ctx, c)
	require.NoError(t, err)

	// Same type, different user → forbidden.
	_, err = svc.Update(ctx, created.ID, "v2", comment.AuthorUser, "u-2")
	require.ErrorIs(t, err, commentsvc.ErrForbidden)

	// Different type → also forbidden.
	_, err = svc.Update(ctx, created.ID, "v2", comment.AuthorAgent, "u-1")
	require.ErrorIs(t, err, commentsvc.ErrForbidden)
}

func TestCommentService_Update_Missing(t *testing.T) {
	svc, _ := setupCommentSvc(t)

	_, err := svc.Update(context.Background(), "c-missing", "v2", comment.AuthorUser, "u-1")
	require.ErrorIs(t, err, comment.ErrNotFound)
}

func TestCommentService_Update_NonTaskTargetForbiddenAsNotFound(t *testing.T) {
	svc, _ := setupCommentSvc(t)
	ctx := context.Background()

	c := &comment.Comment{TargetType: comment.TargetPage, TargetID: "p-1", AuthorID: "u-1", BodyMD: "v1"}
	created, err := svc.Add(ctx, c)
	require.NoError(t, err)

	// Even the original author gets NotFound: the comment is not
	// mounted on a task, so it's "not there" from the task route.
	_, err = svc.Update(ctx, created.ID, "v2", comment.AuthorUser, "u-1")
	require.ErrorIs(t, err, commentsvc.ErrNotFound)
}
