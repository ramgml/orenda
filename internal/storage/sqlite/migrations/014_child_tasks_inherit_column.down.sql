-- ============================================================================
-- 014_child_tasks_inherit_column.down.sql — revert the column_id backfill
-- ============================================================================
-- Phase 14 migrated subtasks into child tasks with column_id=NULL,
-- then this migration backfilled column_id from the parent. The
-- down restores the NULL state so the children re-disappear from
-- the board (matching the pre-014 state).
--
-- We re-NULL every child whose parent has a column_id. Children
-- whose parent was already NULL stay NULL — they were NULL before.

UPDATE tasks AS child
SET column_id = NULL
WHERE child.parent_task_id IS NOT NULL
  AND EXISTS (
    SELECT 1 FROM tasks AS parent
    WHERE parent.id = child.parent_task_id
      AND parent.column_id IS NOT NULL
  );
