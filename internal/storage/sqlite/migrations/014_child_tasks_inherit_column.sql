-- ============================================================================
-- 014_child_tasks_inherit_column.sql — backfill child task column_id
-- ============================================================================
-- Phase 14 (Weeek-style subtasks) lifted `subtasks` into first-class
-- `tasks` rows with `parent_task_id` set. Migration 013 deliberately
-- inserted those rows with `column_id = NULL` because the subtasks
-- table didn't carry column information and the parent task's
-- column_id wasn't always safe to copy (some parents sat in a
-- column the child conceptually didn't belong in).
--
-- After Phase 14 the user wants child tasks visible on the kanban
-- board (UX request). The board groups tasks by `column_id`; a NULL
-- column puts the card in no column at all and it disappears from
-- the user's view. This migration backfills `column_id` for any
-- child whose parent has one, so the existing migrated subtasks
-- surface on the board immediately on `orenda migrate up`.
--
-- Children whose parent has `column_id = NULL` (rare — a top-level
-- task with no board) stay NULL; the UI handles that case as a
-- fallback (they land in the first column of the project).
--
-- New child tasks created after this migration inherit the parent's
-- column in the API layer (createTaskHandler), so they don't slip
-- back into the NULL state.

UPDATE tasks AS child
SET column_id = (
    SELECT parent.column_id
    FROM tasks AS parent
    WHERE parent.id = child.parent_task_id
)
WHERE child.parent_task_id IS NOT NULL
  AND child.column_id IS NULL;
