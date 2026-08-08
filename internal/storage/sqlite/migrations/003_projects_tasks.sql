-- ============================================================================
-- 003_projects_tasks.sql — projects/boards/columns/tasks indexes + triggers
-- ============================================================================
-- Phase 1, task 1.2.
--
-- Like 002_auth.sql, this migration is purely ADDITIVE. All relevant tables
-- (projects, boards, columns, tasks, subtasks, checklists, checklist_items,
-- task_tags, tags) were created in 001_init.sql.
--
-- We add:
--   * idx_tasks_project_column_position — composite index for the kanban
--     board query (Phase 2 will be the heaviest user).
--   * idx_tasks_assignee_status — agent inbox view (status=todo filtered
--     by assignee).
--   * trg_projects_touch, trg_tasks_touch — keep updated_at in sync on UPDATE.
--
-- CHECK constraints on enum-like columns (status, priority, awaiting) are
-- not added because SQLite has no ALTER TABLE … ADD CONSTRAINT. They are
-- enforced at the application layer (Phase 1.4 repositories + Phase 1.7
-- handlers). A future migration could recreate the table if the enum gains
-- a value, but for now keep the schema stable.

CREATE INDEX IF NOT EXISTS idx_tasks_project_column_position
    ON tasks(project_id, column_id, position);

CREATE INDEX IF NOT EXISTS idx_tasks_assignee_status
    ON tasks(assignee_type, assignee_id, status);

CREATE TRIGGER IF NOT EXISTS trg_projects_touch
AFTER UPDATE ON projects
FOR EACH ROW
BEGIN
    UPDATE projects SET updated_at = datetime('now') WHERE id = OLD.id;
END;

CREATE TRIGGER IF NOT EXISTS trg_tasks_touch
AFTER UPDATE ON tasks
FOR EACH ROW
BEGIN
    UPDATE tasks SET updated_at = datetime('now') WHERE id = OLD.id;
END;