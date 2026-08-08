package sqlite

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ramgml/orenda/internal/domain/comment"
)

func TestCommentRepo_CreateAndExtractMentions(t *testing.T) {
	db := setupAgentDB(t)
	repo := NewCommentRepository(db)

	c := &comment.Comment{
		TargetID: "t-1",
		AuthorID: "u-1",
		BodyMD:   "hello @user:alice and @agent:qwen! also @user:bob.",
	}
	got, err := repo.Create(context.Background(), c)
	require.NoError(t, err)
	assert.NotEmpty(t, got.ID)
	assert.NotZero(t, got.CreatedAt)

	mentions, err := repo.MentionsForComment(context.Background(), got.ID)
	require.NoError(t, err)
	require.Len(t, mentions, 3)

	// Order should match the body order: alice/user, qwen/agent, bob/user.
	want := []struct {
		id   string
		kind string
	}{
		{"alice", "user"},
		{"qwen", "agent"},
		{"bob", "user"},
	}
	for i, w := range want {
		assert.Equal(t, w.id, mentions[i].TargetID, "mention %d id", i)
		assert.Equal(t, w.kind, string(mentions[i].TargetType), "mention %d kind", i)
	}
}

func TestCommentRepo_NoMentions(t *testing.T) {
	db := setupAgentDB(t)
	repo := NewCommentRepository(db)

	c := &comment.Comment{TargetID: "t", AuthorID: "u", BodyMD: "no mentions here"}
	got, err := repo.Create(context.Background(), c)
	require.NoError(t, err)

	mentions, err := repo.MentionsForComment(context.Background(), got.ID)
	require.NoError(t, err)
	assert.Empty(t, mentions)
}

func TestCommentRepo_ListByTarget(t *testing.T) {
	db := setupAgentDB(t)
	repo := NewCommentRepository(db)

	for i := 0; i < 3; i++ {
		_, err := repo.Create(context.Background(), &comment.Comment{
			TargetID: "t-1", AuthorID: "u", BodyMD: "msg",
		})
		require.NoError(t, err)
	}
	_, err := repo.Create(context.Background(), &comment.Comment{
		TargetID: "t-2", AuthorID: "u", BodyMD: "other",
	})
	require.NoError(t, err)

	got, err := repo.ListByTarget(context.Background(), comment.TargetTask, "t-1")
	require.NoError(t, err)
	assert.Len(t, got, 3)

	got, err = repo.ListByTarget(context.Background(), comment.TargetTask, "t-2")
	require.NoError(t, err)
	assert.Len(t, got, 1)
}

func TestCommentRepo_ValidateError(t *testing.T) {
	db := setupAgentDB(t)
	repo := NewCommentRepository(db)

	_, err := repo.Create(context.Background(), &comment.Comment{TargetID: "", AuthorID: "u", BodyMD: "x"})
	require.Error(t, err)
}

func TestExtractMentions_IgnoresEmailsAndPlainAts(t *testing.T) {
	body := "email a@b.com and @plainuser and @user:alice and @agent:qwen"
	got := extractMentions(body)
	require.Len(t, got, 2)
	assert.Equal(t, "alice", got[0].TargetID)
	assert.Equal(t, "user", string(got[0].TargetType))
	assert.Equal(t, "qwen", got[1].TargetID)
	assert.Equal(t, "agent", string(got[1].TargetType))
	_ = strings.Contains(body, "@plainuser") // referenced in the literal above
}
