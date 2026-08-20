-- ============================================================================
-- 024_task_created_by.down.sql — Phase 33.2 reverse
-- ============================================================================
-- Destructive by design: dropping created_by_type/created_by_id loses
-- the creator attribution forever. The activity feed (task_activity
-- rows with action='task.created') carries the same signal, so the
-- loss is recoverable from there — but only after the activity was
-- recorded. Treat down as a data-loss event.

DROP INDEX IF EXISTS idx_tasks_created_by;
ALTER TABLE tasks DROP COLUMN created_by_id;
ALTER TABLE tasks DROP COLUMN created_by_type;
