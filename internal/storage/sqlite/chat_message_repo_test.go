package sqlite

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ramgml/orenda/internal/domain/chat"
)

// TestChatThreads_PerUserOwnership exercises migration 047 + the
// thread repo: upsert is idempotent, ownership is per user.
func TestChatThreads_PerUserOwnership(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()
	threads := NewChatThreadRepository(db)
	ctx := context.Background()

	require.NoError(t, threads.Upsert(ctx, "u1", "default"))
	require.NoError(t, threads.Upsert(ctx, "u1", "default"), "upsert is idempotent")

	owned, err := threads.Owned(ctx, "u1", "default")
	require.NoError(t, err)
	assert.True(t, owned)

	owned, err = threads.Owned(ctx, "u2", "default")
	require.NoError(t, err)
	assert.False(t, owned, "another user does not own the thread")

	owned, err = threads.Owned(ctx, "u1", "missing")
	require.NoError(t, err)
	assert.False(t, owned)
}

// TestChatMessages_UserScoped exercises migrations 048/049 through
// the repo: user_id persists, Last/ListByUserThread/PendingThreads
// are scoped to one user, and PendingThreads derives the queue.
func TestChatMessages_UserScoped(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()
	repo := NewChatMessageRepository(db).(*chatMessageRepo)
	ctx := context.Background()

	mk := func(user, thread, sender, body string) {
		require.NoError(t, repo.Create(ctx, &chat.Message{
			UserID: user, ThreadID: thread, SenderType: chat.SenderType(sender), BodyMD: body,
		}))
	}
	mk("u1", "default", "user", "q1")
	mk("u1", "default", "agent", "a1")
	mk("u1", "default", "user", "q2")
	mk("u2", "default", "user", "other-user-question")

	last, err := repo.Last(ctx, "u1", "default")
	require.NoError(t, err)
	require.NotNil(t, last)
	assert.Equal(t, "q2", last.BodyMD, "Last is the newest row for (u1, default)")

	last, err = repo.Last(ctx, "ghost", "default")
	require.NoError(t, err)
	assert.Nil(t, last, "no rows → nil, not an error")

	msgs, err := repo.ListByUserThread(ctx, "u1", "default", 50)
	require.NoError(t, err)
	require.Len(t, msgs, 3)
	for _, m := range msgs {
		assert.Equal(t, "u1", m.UserID, "replay never leaks other users' rows")
		assert.NotEmpty(t, m.ID)
	}

	pending, err := repo.PendingThreads(ctx)
	require.NoError(t, err)
	// Both users have a question pending (u1's newest row is q2,
	// a user message; u2 asked once and nobody answered).
	assert.Len(t, pending, 2)
	byUser := map[string]string{}
	for _, k := range pending {
		last, err := repo.Last(ctx, k.UserID, k.ThreadID)
		require.NoError(t, err)
		byUser[k.UserID] = last.BodyMD
	}
	assert.Equal(t, "q2", byUser["u1"])
	assert.Equal(t, "other-user-question", byUser["u2"])
}
