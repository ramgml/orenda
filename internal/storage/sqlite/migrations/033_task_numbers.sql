-- ============================================================================
-- 033_task_numbers.sql — human-readable sequential task numbers
-- ============================================================================
-- Every task gets a small monotonically-increasing integer (`number`)
-- alongside its UUID. Agents and humans reference tasks as "#42" in
-- conversation, branch names, commit messages and PR titles; the agent
-- surface (REST /api/v1/agent/tasks/{id}/*, the `orenda agent` CLI,
-- the MCP tools) accepts either form and resolves "#42" / "42"
-- through this column.
--
-- Assignment rule for NEW rows: taskRepo.Create draws the next number
-- from the `task_number_seq` high-watermark (UPDATE ... RETURNING in
-- the same transaction as the INSERT). A bare COALESCE(MAX(number),0)+1
-- over `tasks` would REUSE a number after the newest task is deleted —
-- a "#42" reference in a commit message or branch name has to keep
-- pointing at the same task forever, so the watermark never moves
-- backwards. Numbers are NEVER reused after a delete.
--
-- Why the three-step shape for the column: SQLite's ALTER TABLE ...
-- ADD COLUMN ... NOT NULL requires a CONSTANT default, so the column
-- lands as DEFAULT 0; the backfill UPDATE then rewrites existing rows
-- in (created_at, rowid) order via ROW_NUMBER() so the oldest task is
-- #1; the UNIQUE index is created only after the backfill so the
-- transient 0s never collide. The whole body runs in one transaction
-- (runner default), so a concurrent reader never observes the
-- half-backfilled state.

ALTER TABLE tasks ADD COLUMN number INTEGER NOT NULL DEFAULT 0;

UPDATE tasks SET number = (
    SELECT numbered.rn FROM (
        SELECT id, ROW_NUMBER() OVER (ORDER BY created_at, rowid) AS rn
        FROM tasks
    ) AS numbered
    WHERE numbered.id = tasks.id
);

CREATE UNIQUE INDEX idx_tasks_number ON tasks(number);

-- High-watermark for new assignments. One row, id pinned to 1;
-- `next` is the number the following Create will draw. Seeded past
-- the backfill so existing numbers are never re-issued.
CREATE TABLE task_number_seq (
    id   INTEGER PRIMARY KEY CHECK (id = 1),
    next INTEGER NOT NULL
);
INSERT INTO task_number_seq (id, next)
SELECT 1, COALESCE(MAX(number), 0) + 1 FROM tasks;
