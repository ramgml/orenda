-- ============================================================================
-- 007_time_entries_actor.sql — widen time_entries to accept either an agent
-- or a user as the actor
-- ============================================================================
-- Phase 4.6 fix.
--
-- 001_init.sql created time_entries.agent_id as TEXT REFERENCES agents(id).
-- That's correct when only agents have timers, but the Phase 4 UI lets the
-- human owner start/stop timers too — the agent_id field then fails the FK
-- because the row references users(id), not agents(id).
--
-- We fix this by recreating the table with a wider constraint. SQLite has
-- no ALTER TABLE … DROP CONSTRAINT, so we follow the standard
-- rename-and-recreate pattern. The migration runner wraps everything in
-- a transaction already; explicit BEGIN/COMMIT here would conflict.

-- 1. New table without the FK on agent_id.
CREATE TABLE time_entries_v2 (
    id              TEXT PRIMARY KEY,
    task_id         TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    agent_id        TEXT,                    -- user.id or agent.id (no FK)
    started_at      TEXT NOT NULL,
    ended_at        TEXT,
    duration_s      INTEGER,
    source          TEXT NOT NULL DEFAULT 'manual'
);

-- 2. Copy existing rows (no rows exist in production at this point,
--    but the migration is idempotent).
INSERT INTO time_entries_v2 (id, task_id, agent_id, started_at, ended_at, duration_s, source)
SELECT id, task_id, agent_id, started_at, ended_at, duration_s, source FROM time_entries;

-- 3. Drop the old table.
DROP TABLE time_entries;

-- 4. Rename.
ALTER TABLE time_entries_v2 RENAME TO time_entries;

-- 5. Recreate the indexes 006 added.
CREATE INDEX IF NOT EXISTS idx_time_task             ON time_entries(task_id);
CREATE INDEX IF NOT EXISTS idx_time_entries_agent   ON time_entries(agent_id, started_at);
CREATE INDEX IF NOT EXISTS idx_time_entries_open    ON time_entries(agent_id) WHERE ended_at IS NULL;