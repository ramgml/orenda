package sqlite

import (
	"context"
	"database/sql"
	"fmt"
)

// EnsureChatActor idempotently seeds the synthetic "chat" actor the
// dashboard-chat pipeline (internal/api/handlers_chat.go,
// dispatchChatCommand) depends on:
//
//   - study_proposals.created_by_agent has a FK to agents(id)
//     (022_study_planning.sql), so the "/plan day" command — which
//     calls StudyService.Propose(ctx, "chat", …) — fails with a
//     FOREIGN KEY error on any instance where the agents row
//     id='chat' is missing;
//   - the chat actor is also the addressee of agent dialog replies
//     (chatdialog loop, T9).
//
// Precedent: the agent service's ensureOwner
// (internal/service/agent/agent.go) lazily creates the synthetic
// agent-owner@orenda.local user at runtime with constant
// credentials; 044_agent_owner_system_role.sql only heals the role
// on existing rows. The same split applies here:
//
//   - users: role='system' (T171 hardening — no login path),
//     password constant "unusable" (invalid bcrypt — login
//     impossible);
//   - api_tokens: hash constant "unusable", never used for auth;
//   - agents: the FK target itself, status='offline',
//     max_concurrent=1, description written explicitly (scanAgentRow
//     in agent_repo.go scans it into a plain Go string — NULL breaks
//     List).
//
// This runs at runtime, NOT in a migration, on purpose: the 015
// invariant (pinned by TestUserRepo_List_EmptyAndOrdered) is that a
// freshly migrated database contains no users — synthetic accounts
// are created lazily when the feature that needs them first runs.
// INSERT OR IGNORE makes the seed a no-op on already-seeded
// instances and leaves a real agent row id='chat' (if an operator
// ever created one) untouched.
func EnsureChatActor(ctx context.Context, db *sql.DB) error {
	stmts := []string{
		`INSERT OR IGNORE INTO users (id, email, password_hash, display_name, role)
			VALUES ('u-chat', 'chat-agent@orenda.local', 'unusable', 'Dashboard chat agent', 'system')`,
		`INSERT OR IGNORE INTO api_tokens (id, user_id, name, hash, scopes)
			VALUES ('t-chat', 'u-chat', 'chat-actor-seed', 'unusable', '[]')`,
		`INSERT OR IGNORE INTO agents (id, name, type, description, token_id, max_concurrent, status)
			VALUES ('chat', 'chat', '[]', 'dashboard-chat pipeline actor', 't-chat', 1, 'offline')`,
	}
	for _, q := range stmts {
		if _, err := db.ExecContext(ctx, q); err != nil {
			return fmt.Errorf("ensure chat actor: %w", err)
		}
	}
	return nil
}
