-- ============================================================================
-- 003_projects_tasks.down.sql — drop indexes + triggers added by 003
-- ============================================================================

DROP INDEX IF EXISTS idx_tasks_project_column_position;
DROP INDEX IF EXISTS idx_tasks_assignee_status;
DROP TRIGGER IF EXISTS trg_projects_touch;
DROP TRIGGER IF EXISTS trg_tasks_touch;
