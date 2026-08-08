-- ============================================================================
-- 004_agents.sql — agent indexes + heartbeat status helper
-- ============================================================================
-- Phase 3, task 3.1.
--
-- Like 002_auth.sql, this migration is purely ADDITIVE. agents and task_locks
-- tables were created in 001_init.sql.
--
-- We add:
--   * idx_agents_status — heartbeat / dashboard view (status='online')
--   * idx_agents_last_seen — stale-agent sweep by StatusCalculator
--   * trg_agents_touch — keep updated_at accurate on UPDATE (agents
--     table doesn't have one in 001; this is a no-op until Phase 6 adds
--     the column via a future migration)
--
-- task_locks has a PK on task_id; we add an index on agent_id so the
-- "what is this agent working on?" view is O(log n).

CREATE INDEX IF NOT EXISTS idx_agents_status
    ON agents(status);

CREATE INDEX IF NOT EXISTS idx_agents_last_seen
    ON agents(last_seen_at);

CREATE INDEX IF NOT EXISTS idx_task_locks_agent
    ON task_locks(agent_id);

-- Note: the agents table has no updated_at column in 001_init. If a
-- future migration adds one, a trg_agents_touch trigger should land
-- alongside it (see 002_auth.sql/trg_users_touch for the pattern).