-- ============================================================================
-- 050_chat_actor_seed.sql — dashboard-chat pipeline actor (task 9)
-- ============================================================================
-- The dashboard chat command pipeline (internal/api/handlers_chat.go,
-- dispatchChatCommand) stamps study proposals with the actor id "chat":
--
--     StudyService.Propose(ctx, "chat", …)
--
-- study_proposals.created_by_agent has a FK to agents(id)
-- (022_study_planning.sql), so on any instance where no agents row with
-- id='chat' exists the /plan day command fails with a FOREIGN KEY error.
-- Before the T9 dispatch fix this failure was invisible (the unreachable
-- "/plan day" case fell through to the default acknowledgement); after
-- the fix it would fail loudly on live instances.
--
-- Precedent: 012_events_to_tasks.sql seeds its synthetic inbox user with
-- INSERT OR IGNORE; 044_agent_owner_system_role.sql (T171) established
-- the constant-credentials + role='system' convention for synthetic
-- accounts.
--
--   * users: role='system' (T171 hardening — no login path), password
--     constant "unusable" (invalid bcrypt — login impossible);
--   * api_tokens: hash constant "unusable", no expiry needed (never used
--     for auth);
--   * agents: the FK target itself, status='offline', max_concurrent=1.
--
-- INSERT OR IGNORE makes the seed idempotent: re-running on a seeded
-- instance is a no-op, and a real agent row with id='chat' (if an
-- operator ever created one) is left untouched.
-- ============================================================================

INSERT OR IGNORE INTO users (id, email, password_hash, display_name, role)
VALUES ('u-chat', 'chat-agent@orenda.local', 'unusable', 'Dashboard chat agent', 'system');

INSERT OR IGNORE INTO api_tokens (id, user_id, name, hash, scopes)
VALUES ('t-chat', 'u-chat', 'chat-actor-seed', 'unusable', '[]');

INSERT OR IGNORE INTO agents (id, name, type, token_id, max_concurrent, status)
VALUES ('chat', 'chat', '[]', 't-chat', 1, 'offline');
