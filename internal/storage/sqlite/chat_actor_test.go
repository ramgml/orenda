package sqlite

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// EnsureChatActor is the runtime replacement for the reverted
// migration 050: the dashboard-chat pipeline needs an agents row
// id='chat' (study_proposals.created_by_agent FK), but the 015
// invariant — pinned by TestUserRepo_List_EmptyAndOrdered — forbids
// migrations from creating users. Synthetic accounts are seeded
// lazily at runtime, by the ensureOwner precedent
// (agent-owner@orenda.local, internal/service/agent).

func TestChatActor_IdempotentSeed(t *testing.T) {
	db := setupUserDB(t)
	ctx := context.Background()

	require.NoError(t, EnsureChatActor(ctx, db))
	require.NoError(t, EnsureChatActor(ctx, db), "second call must be a no-op, not an error")

	// Exactly one row per seeded table, with the pinned values.
	var users, tokens, agents int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT count(*) FROM users WHERE email = 'chat-agent@orenda.local'`).Scan(&users))
	require.NoError(t, db.QueryRowContext(ctx, `SELECT count(*) FROM api_tokens WHERE id = 't-chat'`).Scan(&tokens))
	require.NoError(t, db.QueryRowContext(ctx, `SELECT count(*) FROM agents WHERE id = 'chat'`).Scan(&agents))
	assert.Equal(t, 1, users, "exactly one chat-actor user row after a double ensure")
	assert.Equal(t, 1, tokens, "exactly one chat-actor token row after a double ensure")
	assert.Equal(t, 1, agents, "exactly one chat agent row after a double ensure")

	var role string
	require.NoError(t, db.QueryRowContext(ctx, `SELECT role FROM users WHERE email = 'chat-agent@orenda.local'`).Scan(&role))
	assert.Equal(t, "system", role, "chat actor must carry role='system' (T171 hardening)")
}

func TestChatActor_SatisfiesProposalFK(t *testing.T) {
	db := setupUserDB(t)
	ctx := context.Background()

	require.NoError(t, EnsureChatActor(ctx, db))

	// The FK the whole exercise is about: a study_proposals row
	// stamped with the chat actor must insert cleanly on a foreign-key
	// enabled connection (setupUserDB opens with EnableForeign).
	_, err := db.ExecContext(ctx,
		`INSERT INTO study_proposals (id, course_id, title, target_date, created_by_agent)
		 VALUES ('sp-test', NULL, 'Daily plan', '2026-09-17', 'chat')`)
	require.NoError(t, err, "agents row id='chat' must satisfy study_proposals.created_by_agent FK")
}

func TestChatActor_LeavesForeignRowsUntouched(t *testing.T) {
	db := setupUserDB(t)
	ctx := context.Background()

	// Pre-existing unrelated rows (the way a live instance looks).
	_, err := db.ExecContext(ctx,
		`INSERT INTO users (id, email, password_hash, display_name, role)
		 VALUES ('u-other', 'human@example.com', 'h', 'Human', 'owner')`)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx,
		`INSERT INTO api_tokens (id, user_id, name, hash, scopes)
		 VALUES ('t-other', 'u-other', 'human token', 'h', '[]')`)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx,
		`INSERT INTO agents (id, name, type, description, token_id, max_concurrent, status)
		 VALUES ('a-other', 'worker', '[]', 'human agent', 't-other', 3, 'online')`)
	require.NoError(t, err)

	require.NoError(t, EnsureChatActor(ctx, db))

	var users, tokens, agents int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT count(*) FROM users`).Scan(&users))
	require.NoError(t, db.QueryRowContext(ctx, `SELECT count(*) FROM api_tokens`).Scan(&tokens))
	require.NoError(t, db.QueryRowContext(ctx, `SELECT count(*) FROM agents`).Scan(&agents))
	assert.Equal(t, 2, users, "only the pre-existing user plus the chat actor")
	assert.Equal(t, 2, tokens, "only the pre-existing token plus the chat actor")
	assert.Equal(t, 2, agents, "only the pre-existing agent plus the chat actor")

	// The foreign rows kept their exact values — INSERT OR IGNORE
	// must not rewrite an existing row.
	var name, status string
	var maxConcurrent int
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT name, status, max_concurrent FROM agents WHERE id = 'a-other'`).Scan(&name, &status, &maxConcurrent))
	assert.Equal(t, "worker", name)
	assert.Equal(t, "online", status)
	assert.Equal(t, 3, maxConcurrent)
}
