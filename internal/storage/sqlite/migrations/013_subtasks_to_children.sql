-- ============================================================================
-- 013_subtasks_to_children.sql — fold `subtasks` into `tasks`
-- ============================================================================
-- Phase 14 (Weeek-style subtasks vs checklists).
--
-- Until now Orenda carried two parallel "checkbox under a task" models —
-- `subtasks` (flat, no grouping) and `checklists` (named lists of items).
-- They were indistinguishable to the user and forced the agent API to
-- expose two endpoints for the same conceptual thing.
--
-- Phase 14 keeps `checklists` as the local quick-checkbox primitive and
-- promotes `subtasks` to first-class child tasks via the existing
-- `tasks.parent_task_id` column (which has been in the schema since 001
-- but never written to from the application layer).
--
-- This migration copies every row from `subtasks` into `tasks` as a
-- minimal task (only `id`, `project_id`, `parent_task_id`, `title`,
-- `status`, `position`, `created_at`, `updated_at` set; everything else
-- left at its default) and then drops the `subtasks` table and its
-- index.
--
-- Generated UUIDs use SQLite's `randomblob` (random v4 — UUIDv7 isn't
-- available inside SQL). They're still globally unique; we just lose
-- the time-ordering that v7 gives for the migrated rows. That's
-- acceptable because (a) the migrated set is small (one-shot) and
-- (b) rows are ordered by `position, created_at` everywhere we render.

-- 1. Translate subtasks into tasks.
INSERT INTO tasks (
    id,
    project_id,
    parent_task_id,
    title,
    status,
    priority,
    awaiting,
    time_spent_s,
    position,
    created_at,
    updated_at
)
SELECT
    -- v4 UUID (lowercase hex, 8-4-4-4-12 layout). See header comment.
    lower(
        hex(randomblob(4)) || '-' ||
        hex(randomblob(2)) || '-' ||
        hex(randomblob(2)) || '-' ||
        hex(randomblob(2)) || '-' ||
        hex(randomblob(6))
    ),
    (SELECT project_id FROM tasks AS p WHERE p.id = s.task_id),
    s.task_id,
    s.title,
    CASE WHEN s.done = 1 THEN 'done' ELSE 'todo' END,
    'medium',
    'none',
    0,
    s.position,
    datetime('now'),
    datetime('now')
FROM subtasks AS s;

-- 2. Drop the table and its index.
DROP INDEX IF EXISTS idx_subtasks_task;
DROP TABLE IF EXISTS subtasks;
