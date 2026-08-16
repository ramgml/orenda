-- ============================================================================
-- 004_agents.down.sql — drop agent/task_locks indexes added by 004
-- ============================================================================

DROP INDEX IF EXISTS idx_agents_status;
DROP INDEX IF EXISTS idx_agents_last_seen;
DROP INDEX IF EXISTS idx_task_locks_agent;
