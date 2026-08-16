-- ============================================================================
-- 012_events_to_tasks.sql — fold calendar events into tasks
-- ============================================================================
-- Phase 11 follow-up. Calendar "events" become first-class tasks with a
-- time range: a task with start_at + end_at shows on the calendar, a task
-- without those fields is a plain kanban item. Every event must belong to
-- a project — events without a project get an auto-created "Inbox"
-- project the first time we migrate.
--
-- This migration is the only place we touch the legacy events table —
-- after this, the table is dropped and the Go code reads/writes the
-- `tasks` table for calendar operations.

PRAGMA foreign_keys = ON;

-- ----------------------------------------------------------------------------
-- 1. Add the new columns to tasks.
-- ----------------------------------------------------------------------------

ALTER TABLE tasks ADD COLUMN start_at TEXT;
ALTER TABLE tasks ADD COLUMN end_at   TEXT;
ALTER TABLE tasks ADD COLUMN all_day  INTEGER NOT NULL DEFAULT 0;
ALTER TABLE tasks ADD COLUMN color    TEXT;

-- Index for the calendar's primary query: tasks with non-null start_at
-- in a [from, to] window. Partial index keeps it cheap on big projects.
CREATE INDEX IF NOT EXISTS idx_tasks_time
    ON tasks(start_at, end_at)
    WHERE start_at IS NOT NULL AND end_at IS NOT NULL;

-- ----------------------------------------------------------------------------
-- 2. Make sure every project has a column for timed tasks (the calendar
-- only lists timed tasks, but they live in columns like anything else).
-- Existing projects already ship with backlog/todo/in_progress/review/done.
-- Nothing to do here — we just rely on the existing default columns.
-- ----------------------------------------------------------------------------

-- ----------------------------------------------------------------------------
-- 3. Create the Inbox project. We always create it (idempotent) so
-- the FK on tasks.project_id never points to a missing row, even when
-- this migration runs against an empty events table.
-- ----------------------------------------------------------------------------

INSERT OR IGNORE INTO users (id, email, password_hash, display_name, role, created_at, updated_at)
VALUES (
    '00000000-0000-0000-0000-000000000001',
    'system-inbox@orenda.local',
    '!disabled-inbox-placeholder!',
    'Inbox system',
    'system',
    datetime('now'),
    datetime('now')
);

INSERT OR IGNORE INTO projects (id, name, color, owner_id, archived, created_at, updated_at)
VALUES (
    '00000000-0000-0000-0000-00000000cafe',
    'Inbox',
    '#6b7280',
    COALESCE(
        (SELECT id FROM users WHERE role != 'system' OR id IS NULL
         ORDER BY created_at ASC LIMIT 1),
        '00000000-0000-0000-0000-000000000001'
    ),
    0,
    datetime('now'),
    datetime('now')
);

-- ----------------------------------------------------------------------------
-- 4. Migrate every event into tasks.
--    - Title and description come from the event.
--    - start_at / end_at / all_day / color are the new task columns.
--    - project_id: events with project_id keep theirs; events without
--      land in Inbox.
--    - status: a timed task defaults to 'todo' (it's a scheduled thing,
--      not a backlog item); use the project's default first column.
--    - column_id: resolve via the project's columns. We use the second
--      default column ("todo") when present, otherwise the first.
--    - The event id is preserved as the new task id so any external
--      bookmark or notification link keeps working.
-- ----------------------------------------------------------------------------

INSERT INTO tasks (
    id, project_id, parent_task_id, column_id, title, description,
    status, priority, assignee_type, assignee_id, awaiting,
    context_md, agent_notes,
    due_at, started_at, claimed_at, completed_at,
    time_estimate_s, time_spent_s, position,
    start_at, end_at, all_day, color,
    created_at, updated_at
)
SELECT
    e.id,
    COALESCE(e.project_id, '00000000-0000-0000-0000-00000000cafe'),
    NULL,
    (
      SELECT id FROM columns
       WHERE board_id = (SELECT id FROM boards
                          WHERE project_id = COALESCE(e.project_id, '00000000-0000-0000-0000-00000000cafe')
                          ORDER BY position ASC LIMIT 1)
       ORDER BY position ASC LIMIT 1
    ),
    e.title,
    e.description,
    'todo',
    'medium',
    NULL,
    NULL,
    'none',
    NULL,
    NULL,
    NULL,
    NULL,
    NULL,
    NULL,
    NULL,
    0,
    0.0,
    e.start_at,
    e.end_at,
    e.all_day,
    e.color,
    e.created_at,
    e.updated_at
FROM events e
WHERE NOT EXISTS (
    SELECT 1 FROM tasks t WHERE t.id = e.id
);

-- ----------------------------------------------------------------------------
-- 5. Drop the legacy events table.
-- ----------------------------------------------------------------------------

DROP TABLE IF EXISTS events;