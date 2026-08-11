-- ============================================================================
-- 015_inbox_no_project.sql — Inbox is tasks with project_id IS NULL
-- ============================================================================
-- Phase 16. Before this migration every task had a NOT NULL project_id
-- pointing at a real project row; the calendar used a well-known
-- "system Inbox" project (00000000-0000-0000-0000-00000000cafe) as a
-- placeholder for tasks that hadn't been filed yet. That was a hack —
-- Inbox wasn't really a project (no board, no kanban, no archive) but
-- it sat in the projects table and polluted every "list projects"
-- query.
--
-- This migration removes the system Inbox and makes tasks.project_id
-- nullable. The Inbox is no longer a project — it's the set of tasks
-- where project_id IS NULL. Code that talks about "the inbox" now
-- asks `WHERE project_id IS NULL` instead of `WHERE project_id = '...cafe'`.
--
-- Why so many rebuilds
-- ---------------------
-- SQLite's `ALTER TABLE ... RENAME` rewrites trigger and view
-- references to the new name, but it does NOT rewrite foreign-key
-- references in OTHER tables. After `ALTER TABLE tasks RENAME TO
-- tasks_old`, every child table that declared `REFERENCES tasks(id)`
-- now holds a dangling reference to `tasks_old`. We can't fix this
-- in place — `ALTER TABLE ... RENAME COLUMN` won't touch REFERENCES,
-- and `PRAGMA writable_schema` is too brittle to ship. So we rebuild
-- each child table verbatim and copy rows over with rowid preserved.
--
-- Steps
-- -----
-- 0. Mark the migration as needing foreign_keys=OFF (Phase 16.1 marker).
-- 1. Drop triggers on `tasks` (FTS sync, updated_at).
-- 2. Rebuild every child table that REFERENCES tasks:
--    task_locks, checklists, task_tags, task_activity, time_entries.
-- 3. Rebuild `tasks` itself with project_id nullable.
-- 4. Copy every row from the renamed tables into the fresh tables,
--    preserving rowid (FTS5 external-content mapping requires it).
-- 5. Recreate every index from 001/003/012 and the FTS sync triggers.
--    Rebuild tasks_fts against the new content.
-- 6. Detach legacy Inbox-project rows (project_id = ...cafe): set
--    project_id to NULL and clear column_id (the column belonged to
--    a board that no longer exists).
-- 7. Drop the Inbox project (boards+columns cascade). Drop the
--    placeholder system user ONLY IF no remaining projects reference
--    it (defensive — the Inbox was the only client).

-- orenda:foreign_keys_off

-- Step 1: triggers on tasks (recreated later).
DROP TRIGGER IF EXISTS trg_tasks_touch;
DROP TRIGGER IF EXISTS trg_tasks_fts_insert;
DROP TRIGGER IF EXISTS trg_tasks_fts_update;
DROP TRIGGER IF EXISTS trg_tasks_fts_delete;

-- Step 2: drop indexes (recreated at the end).
DROP INDEX IF EXISTS idx_tasks_project;
DROP INDEX IF EXISTS idx_tasks_status;
DROP INDEX IF EXISTS idx_tasks_assignee;
DROP INDEX IF EXISTS idx_tasks_due;
DROP INDEX IF EXISTS idx_tasks_parent;
DROP INDEX IF EXISTS idx_tasks_project_column_position;
DROP INDEX IF EXISTS idx_tasks_assignee_status;
DROP INDEX IF EXISTS idx_tasks_time;
DROP INDEX IF EXISTS idx_subtasks_task;
DROP INDEX IF EXISTS idx_checklists_task;
DROP INDEX IF EXISTS idx_task_locks_agent;
DROP INDEX IF EXISTS idx_activity_task;
DROP INDEX IF EXISTS idx_time_task;

-- Step 3: rebuild `tasks` itself FIRST. Doing it before the child
-- tables means the new child-table FKs can point at the fresh
-- `tasks` (the alternative — rebuilding children first while `tasks`
-- still has its old name — leaves dangling FKs that point at the
-- soon-to-be-removed tasks_old).
ALTER TABLE tasks RENAME TO tasks_old;
CREATE TABLE tasks (
    id              TEXT PRIMARY KEY,
    project_id      TEXT REFERENCES projects(id) ON DELETE CASCADE,
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
    updated_at      TEXT NOT NULL DEFAULT (datetime('now')),
    start_at        TEXT,
    end_at          TEXT,
    all_day         INTEGER NOT NULL DEFAULT 0,
    color           TEXT,
    -- Phase 23.3: RRULE carried over from the legacy events
    -- table. The migration 012 that folded events into tasks didn't
    -- copy recurrence_rule, so recurring events silently lost
    -- their rule on save. Adding the column now (during the same
    -- tasks rebuild as Phase 16) makes Phase 23.3 expansion work
    -- end-to-end without a follow-up migration. Existing rows have
    -- no recurrence so this stays NULL by default.
    recurrence      TEXT
);
INSERT INTO tasks (
    id, project_id, parent_task_id, column_id, title, description,
    status, priority, assignee_type, assignee_id, awaiting,
    context_md, agent_notes, due_at, started_at, claimed_at, completed_at,
    time_estimate_s, time_spent_s, position,
    start_at, end_at, all_day, color,
    recurrence,
    created_at, updated_at,
    rowid
)
SELECT
    id, project_id, parent_task_id, column_id, title, description,
    status, priority, assignee_type, assignee_id, awaiting,
    context_md, agent_notes, due_at, started_at, claimed_at, completed_at,
    time_estimate_s, time_spent_s, position,
    start_at, end_at, all_day, color,
    NULL,
    created_at, updated_at,
    rowid
FROM tasks_old;
DROP TABLE tasks_old;

-- Step 4: rebuild each child table. `REFERENCES tasks(id)` now
-- resolves to the new (rebuilt) tasks table, so the FKs are real.
-- Rowids are preserved so external-content FTS5 mappings stay
-- intact.

-- task_locks (PK on task_id).
ALTER TABLE task_locks RENAME TO task_locks_old;
CREATE TABLE task_locks (
    task_id         TEXT PRIMARY KEY REFERENCES tasks(id) ON DELETE CASCADE,
    agent_id        TEXT NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
    acquired_at     TEXT NOT NULL DEFAULT (datetime('now'))
);
INSERT INTO task_locks (task_id, agent_id, acquired_at, rowid)
    SELECT task_id, agent_id, acquired_at, rowid FROM task_locks_old;
DROP TABLE task_locks_old;

-- checklists.
ALTER TABLE checklists RENAME TO checklists_old;
CREATE TABLE checklists (
    id              TEXT PRIMARY KEY,
    task_id         TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    title           TEXT NOT NULL,
    position        INTEGER NOT NULL DEFAULT 0
);
INSERT INTO checklists (id, task_id, title, position, rowid)
    SELECT id, task_id, title, position, rowid FROM checklists_old;
DROP TABLE checklists_old;

-- task_tags.
ALTER TABLE task_tags RENAME TO task_tags_old;
CREATE TABLE task_tags (
    task_id         TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    tag_id          TEXT NOT NULL REFERENCES tags(id) ON DELETE CASCADE,
    PRIMARY KEY (task_id, tag_id)
);
INSERT INTO task_tags (task_id, tag_id, rowid)
    SELECT task_id, tag_id, rowid FROM task_tags_old;
DROP TABLE task_tags_old;

-- task_activity.
ALTER TABLE task_activity RENAME TO task_activity_old;
CREATE TABLE task_activity (
    id              TEXT PRIMARY KEY,
    task_id         TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    actor_type      TEXT NOT NULL,
    actor_id        TEXT NOT NULL,
    action          TEXT NOT NULL,
    payload         TEXT,
    created_at      TEXT NOT NULL DEFAULT (datetime('now'))
);
INSERT INTO task_activity (id, task_id, actor_type, actor_id, action, payload, created_at, rowid)
    SELECT id, task_id, actor_type, actor_id, action, payload, created_at, rowid FROM task_activity_old;
DROP TABLE task_activity_old;

-- time_entries. Schema from 007 (agent_id without FK to agents).
ALTER TABLE time_entries RENAME TO time_entries_old;
CREATE TABLE time_entries (
    id              TEXT PRIMARY KEY,
    task_id         TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    agent_id        TEXT,
    started_at      TEXT NOT NULL,
    ended_at        TEXT,
    duration_s      INTEGER,
    source          TEXT NOT NULL DEFAULT 'manual'
);
INSERT INTO time_entries (id, task_id, agent_id, started_at, ended_at, duration_s, source, rowid)
    SELECT id, task_id, agent_id, started_at, ended_at, duration_s, source, rowid FROM time_entries_old;
DROP TABLE time_entries_old;

-- Step 5: recreate indexes on tasks + child tables (verbatim from
-- 001 + 003 + 005 + 006 + 012). We drop indexes from 005/006 along
-- with the rest above; everything comes back here.
CREATE INDEX idx_tasks_project ON tasks(project_id);
CREATE INDEX idx_tasks_status ON tasks(status);
CREATE INDEX idx_tasks_assignee ON tasks(assignee_type, assignee_id);
CREATE INDEX idx_tasks_due ON tasks(due_at);
CREATE INDEX idx_tasks_parent ON tasks(parent_task_id);
CREATE INDEX idx_tasks_project_column_position ON tasks(project_id, column_id, position);
CREATE INDEX idx_tasks_assignee_status ON tasks(assignee_type, assignee_id, status);
CREATE INDEX idx_tasks_time ON tasks(start_at, end_at)
    WHERE start_at IS NOT NULL AND end_at IS NOT NULL;

CREATE INDEX idx_activity_task ON task_activity(task_id, created_at);
CREATE INDEX idx_activity_actor ON task_activity(actor_type, actor_id, created_at);
CREATE INDEX idx_time_task ON time_entries(task_id);
CREATE INDEX idx_time_entries_agent ON time_entries(agent_id, started_at);
CREATE INDEX idx_time_entries_open ON time_entries(agent_id) WHERE ended_at IS NULL;
CREATE INDEX idx_checklists_task ON checklists(task_id);
CREATE INDEX idx_task_locks_agent ON task_locks(agent_id);

-- Step 6: triggers on tasks (verbatim from 003 + 008).
CREATE TRIGGER trg_tasks_touch
AFTER UPDATE ON tasks
FOR EACH ROW
BEGIN
    UPDATE tasks SET updated_at = datetime('now') WHERE id = OLD.id;
END;

CREATE TRIGGER trg_tasks_fts_insert AFTER INSERT ON tasks BEGIN
    INSERT INTO tasks_fts(rowid, title, description, context_md)
    VALUES (new.rowid, new.title, new.description, new.context_md);
END;
CREATE TRIGGER trg_tasks_fts_update AFTER UPDATE ON tasks BEGIN
    INSERT INTO tasks_fts(tasks_fts, rowid, title, description, context_md)
    VALUES ('delete', old.rowid, old.title, old.description, old.context_md);
    INSERT INTO tasks_fts(rowid, title, description, context_md)
    VALUES (new.rowid, new.title, new.description, new.context_md);
END;
CREATE TRIGGER trg_tasks_fts_delete AFTER DELETE ON tasks BEGIN
    INSERT INTO tasks_fts(tasks_fts, rowid, title, description, context_md)
    VALUES ('delete', old.rowid, old.title, old.description, old.context_md);
END;

-- Force the FTS index to reflect the rebuilt table. Cheaper than
-- re-emitting INSERTs through every trigger we just recreated.
INSERT INTO tasks_fts(tasks_fts) VALUES('rebuild');

-- Step 7: detach the legacy Inbox project's tasks. They were the only
-- users of the ...cafe project id; the project itself disappears in
-- step 8. column_id is also cleared because the project's board (and
-- its columns) is dropped with it.
UPDATE tasks
SET project_id = NULL,
    column_id = NULL
WHERE project_id = '00000000-0000-0000-0000-00000000cafe';

-- Step 8: drop the Inbox project (boards + columns CASCADE). Then the
-- placeholder system user, but only if no remaining project references
-- it (defensive — the Inbox was the only client).
DELETE FROM projects WHERE id = '00000000-0000-0000-0000-00000000cafe';

DELETE FROM users
WHERE id = '00000000-0000-0000-0000-000000000001'
  AND role = 'system'
  AND NOT EXISTS (
      SELECT 1 FROM projects WHERE owner_id = '00000000-0000-0000-0000-000000000001'
  );
