-- ============================================================================
-- 007_time_entries_actor.down.sql — recreate the FK on agent_id
-- ============================================================================
-- Mirror of the up migration in reverse. SQLite can't drop a column
-- constraint in place; we recreate the old shape (agent_id → agents.id)
-- and copy rows back. The recreate path mirrors 007's up: rename +
-- recreate. Foreign-key OFF is required to drop the new shape before
-- reattaching the FK to agents (the table isn't referenced by anything
-- downstream but we keep the convention from Phase 16).
--
-- orenda:foreign_keys_off

CREATE TABLE time_entries_v1 (
    id              TEXT PRIMARY KEY,
    task_id         TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    agent_id        TEXT NOT NULL REFERENCES agents(id) ON DELETE SET NULL,
    started_at      TEXT NOT NULL,
    ended_at        TEXT,
    duration_s      INTEGER,
    source          TEXT NOT NULL DEFAULT 'manual'
);

INSERT INTO time_entries_v1 (id, task_id, agent_id, started_at, ended_at, duration_s, source)
SELECT id, task_id, agent_id, started_at, ended_at, duration_s, source FROM time_entries;

DROP TABLE time_entries;

ALTER TABLE time_entries_v1 RENAME TO time_entries;

CREATE INDEX IF NOT EXISTS idx_time_task ON time_entries(task_id);
CREATE INDEX IF NOT EXISTS idx_time_entries_agent ON time_entries(agent_id, started_at);
CREATE INDEX IF NOT EXISTS idx_time_entries_open ON time_entries(agent_id) WHERE ended_at IS NULL;
