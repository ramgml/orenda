-- ============================================================================
-- 012_events_to_tasks.down.sql — recreate the events table and migrate back
-- ============================================================================
-- The up migration moved every event row into tasks with start_at/end_at
-- populated; the down rebuilds the events table from those timed tasks
-- and drops the calendar columns on tasks.
--
-- orenda:foreign_keys_off
-- the events table is being recreated with FK to projects; during
-- the recreate the live tasks rows still reference columns. Phase 16's
-- FK-OFF recipe applies here too.

-- ----------------------------------------------------------------------------
-- 1. Recreate the events table with the original shape.
-- ----------------------------------------------------------------------------

CREATE TABLE events (
    id              TEXT PRIMARY KEY,
    title           TEXT NOT NULL,
    description     TEXT,
    start_at        TEXT NOT NULL,
    end_at          TEXT NOT NULL,
    all_day         INTEGER NOT NULL DEFAULT 0,
    color           TEXT,
    project_id      TEXT REFERENCES projects(id) ON DELETE SET NULL,
    recurrence_rule TEXT,
    parent_event_id TEXT REFERENCES events(id) ON DELETE SET NULL,
    created_at      TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at      TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX idx_events_start ON events(start_at);

-- ----------------------------------------------------------------------------
-- 2. Copy every timed task back into events.
--    We re-create one row per timed task that came from this migration
--    (i.e. tasks with both start_at and end_at set). The id stays the
--    same so any external bookmark keeps working.
-- ----------------------------------------------------------------------------

INSERT INTO events (id, title, description, start_at, end_at, all_day, color, project_id, created_at, updated_at)
SELECT
    id, title, description, start_at, end_at, all_day, color, project_id, created_at, updated_at
FROM tasks
WHERE start_at IS NOT NULL AND end_at IS NOT NULL;

-- ----------------------------------------------------------------------------
-- 3. Drop the calendar columns from tasks (and the partial time index).
-- ----------------------------------------------------------------------------

DROP INDEX IF EXISTS idx_tasks_time;

-- SQLite has no DROP COLUMN; recreate the tasks table without the
-- calendar columns. The column list mirrors 001_init (no `color` —
-- it was added by 012 itself, no `recurrence` — that's a Go-only field
-- not yet on disk; the rest mirrors 001_init).
--
-- This is the standard Phase-16 recipe.

CREATE TABLE tasks_v2 (
    id              TEXT PRIMARY KEY,
    project_id      TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    parent_task_id  TEXT REFERENCES tasks(id) ON DELETE CASCADE,
    column_id       TEXT REFERENCES columns(id) ON DELETE SET NULL,
    title           TEXT NOT NULL,
    description     TEXT,
    status          TEXT NOT NULL DEFAULT 'todo',
    priority        TEXT NOT NULL DEFAULT 'medium',
    assignee_type   TEXT,
    assignee_id     TEXT,
    awaiting        TEXT NOT NULL DEFAULT 'none',
    context_md      TEXT,
    agent_notes     TEXT,
    due_at          TEXT,
    started_at      TEXT,
    claimed_at      TEXT,
    completed_at    TEXT,
    time_estimate_s INTEGER,
    time_spent_s    INTEGER NOT NULL DEFAULT 0,
    position        REAL NOT NULL DEFAULT 0,
    created_at      TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at      TEXT NOT NULL DEFAULT (datetime('now'))
);

INSERT INTO tasks_v2 (id, project_id, parent_task_id, column_id, title, description, status, priority, assignee_type, assignee_id, awaiting, context_md, agent_notes, due_at, started_at, claimed_at, completed_at, time_estimate_s, time_spent_s, position, created_at, updated_at)
SELECT id, project_id, parent_task_id, column_id, title, description, status, priority, assignee_type, assignee_id, awaiting, context_md, agent_notes, due_at, started_at, claimed_at, completed_at, time_estimate_s, time_spent_s, position, created_at, updated_at
FROM tasks;

DROP TABLE tasks;
ALTER TABLE tasks_v2 RENAME TO tasks;

-- The other triggers and indexes (003_projects_tasks etc.) were IF
-- NOT EXISTS, so they stay valid through the recreate. The FTS sync
-- triggers from 008_wiki keep working since they touch the same column
-- names.
