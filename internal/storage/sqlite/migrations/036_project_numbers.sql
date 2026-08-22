-- ============================================================================
-- 036_project_numbers.sql — human-readable sequential project numbers
-- ============================================================================
-- Every project gets a small monotonically-increasing integer (`number`)
-- alongside its UUID. The agent surface and the UI reference projects as
-- "P7" in conversation, branch names, commit messages and PR titles; the
-- agent surface (REST /api/v1/agent/projects/{id}/*, the MCP tools) and
-- the user surface accept either form and resolve "P7" through this
-- column.
--
-- Assignment rule for NEW rows: projectRepo.Create draws the next number
-- from the `project_number_seq` high-watermark (UPDATE ... RETURNING in
-- the same transaction as the INSERT). A bare COALESCE(MAX(number),0)+1
-- over `projects` would REUSE a number after the newest project is
-- deleted — a "P7" reference in a commit message or branch name has to
-- keep pointing at the same project forever, so the watermark never moves
-- backwards. Numbers are NEVER reused after a delete.
--
-- Why the three-step shape for the column: SQLite's ALTER TABLE ...
-- ADD COLUMN ... NOT NULL requires a CONSTANT default, so the column
-- lands as DEFAULT 0; the backfill UPDATE then rewrites existing rows
-- in (created_at, rowid) order via ROW_NUMBER() so the oldest project is
-- #1; the UNIQUE index is created only after the backfill so the
-- transient 0s never collide. The whole body runs in one transaction
-- (runner default), so a concurrent reader never observes the
-- half-backfilled state.

ALTER TABLE projects ADD COLUMN number INTEGER NOT NULL DEFAULT 0;

UPDATE projects SET number = (
    SELECT numbered.rn FROM (
        SELECT id, ROW_NUMBER() OVER (ORDER BY created_at, rowid) AS rn
        FROM projects
    ) AS numbered
    WHERE numbered.id = projects.id
);

CREATE UNIQUE INDEX idx_projects_number ON projects(number);

-- High-watermark for new assignments. One row, id pinned to 1;
-- `next` is the number the following Create will draw. Seeded past
-- the backfill so existing numbers are never re-issued.
CREATE TABLE project_number_seq (
    id   INTEGER PRIMARY KEY CHECK (id = 1),
    next INTEGER NOT NULL
);
INSERT INTO project_number_seq (id, next)
SELECT 1, COALESCE(MAX(number), 0) + 1 FROM projects;
