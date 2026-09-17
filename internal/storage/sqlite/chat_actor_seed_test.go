package sqlite

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestChatActorSeed_Idempotent exercises migration 050: the seeded
// "chat" actor satisfies the study_proposals.created_by_agent FK
// used by the dashboard-chat pipeline, and re-applying the seed is
// a no-op that never duplicates rows or clobbers a real agent.
func TestChatActorSeed_Idempotent(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()
	ctx := context.Background()

	seed, err := MigrationsFS.ReadFile("migrations/050_chat_actor_seed.sql")
	require.NoError(t, err)

	for i := 0; i < 2; i++ {
		_, err = db.ExecContext(ctx, string(seed))
		require.NoError(t, err, "seed apply %d must succeed (idempotent)", i+1)
	}

	var agents, tokens, users int
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM agents WHERE id='chat'`).Scan(&agents))
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM api_tokens WHERE id='t-chat'`).Scan(&tokens))
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM users WHERE id='u-chat'`).Scan(&users))
	assert.Equal(t, 1, agents, "exactly one chat agent row")
	assert.Equal(t, 1, tokens, "exactly one chat token row")
	assert.Equal(t, 1, users, "exactly one chat user row")

	// The seed must satisfy the FK the /plan day path hits.
	_, err = db.ExecContext(ctx,
		`INSERT INTO study_proposals (id, title, target_date, created_by_agent)
		 VALUES ('p-seed-check', 'Daily plan', '2026-01-01', 'chat')`)
	require.NoError(t, err, "seeded agent must satisfy created_by_agent FK")

	// A pre-existing operator-owned row is left untouched.
	_, err = db.ExecContext(ctx,
		`INSERT INTO users (id, email, password_hash, display_name) VALUES ('u-op', 'op@x', 'x', 'Op')`)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx,
		`INSERT INTO api_tokens (id, user_id, name, hash, scopes) VALUES ('t-op', 'u-op', 'n', 'h', '[]')`)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx,
		`INSERT INTO agents (id, name, type, token_id, max_concurrent) VALUES ('chat', 'operator-chat', '[]', 't-op', 3)`)
	require.Error(t, err, "seeded id is PK-protected — operator cannot silently take it")
}

// TestChatActorSeed_Down pins the .down.sql guard: the agents row
// is removed only once no proposal references it (the DELETE is
// FK-guarded); token and user always go.
func TestChatActorSeed_Down(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()
	ctx := context.Background()

	seed, err := MigrationsFS.ReadFile("migrations/050_chat_actor_seed.sql")
	require.NoError(t, err)
	down, err := MigrationsFS.ReadFile("migrations/050_chat_actor_seed.down.sql")
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, string(seed))
	require.NoError(t, err)

	// A proposal referencing the actor makes the agents DELETE a
	// no-op (the WHERE NOT EXISTS guard), but the unconditional
	// token/user deletes must still not break the FK chain —
	// with the proposal in place they are blocked too.
	_, err = db.ExecContext(ctx,
		`INSERT INTO study_proposals (id, title, target_date, created_by_agent)
		 VALUES ('p-block', 'D', '2026-01-01', 'chat')`)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, string(down))
	require.Error(t, err, "down must fail while the proposal references the chain")

	// Once the referencing proposal is gone, down removes all rows.
	_, err = db.ExecContext(ctx, `DELETE FROM study_proposals WHERE id='p-block'`)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, string(down))
	require.NoError(t, err)
	var agents, tokens, users int
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM agents WHERE id='chat'`).Scan(&agents))
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM api_tokens WHERE id='t-chat'`).Scan(&tokens))
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM users WHERE id='u-chat'`).Scan(&users))
	assert.Equal(t, 0, agents, "agent row is removed once unreferenced")
	assert.Equal(t, 0, tokens, "token goes on down")
	assert.Equal(t, 0, users, "user goes on down")
}
