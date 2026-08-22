-- ============================================================================
-- 038_course_numbers.sql — human-readable sequential course numbers
-- ============================================================================
-- Every course gets a small monotonically-increasing integer (`number`)
-- alongside its UUID. The agent surface and the UI reference courses as
-- "C7" in conversation, branch names, commit messages and PR titles; the
-- agent surface (REST /api/v1/agent/courses/{id}/*, the MCP tools) and
-- the user surface accept either form and resolve "C7" through this
-- column.
--
-- Assignment rule for NEW rows: courseRepo.CreateCourse draws the next
-- number from the `course_number_seq` high-watermark (UPDATE ... RETURNING
-- in the same transaction as the INSERT). A bare COALESCE(MAX(number),0)+1
-- over `courses` would REUSE a number after the newest course is deleted —
-- a "C7" reference in a commit message or branch name has to keep pointing
-- at the same course forever, so the watermark never moves backwards.
-- Numbers are NEVER reused after a delete.
--
-- Why the three-step shape for the column: SQLite's ALTER TABLE ...
-- ADD COLUMN ... NOT NULL requires a CONSTANT default, so the column
-- lands as DEFAULT 0; the backfill UPDATE then rewrites existing rows
-- in (created_at, rowid) order via ROW_NUMBER() so the oldest course is
-- #1; the UNIQUE index is created only after the backfill so the
-- transient 0s never collide. The whole body runs in one transaction
-- (runner default), so a concurrent reader never observes the
-- half-backfilled state.

ALTER TABLE courses ADD COLUMN number INTEGER NOT NULL DEFAULT 0;

UPDATE courses SET number = (
    SELECT numbered.rn FROM (
        SELECT id, ROW_NUMBER() OVER (ORDER BY created_at, rowid) AS rn
        FROM courses
    ) AS numbered
    WHERE numbered.id = courses.id
);

CREATE UNIQUE INDEX idx_courses_number ON courses(number);

-- High-watermark for new assignments. One row, id pinned to 1;
-- `next` is the number the following Create will draw. Seeded past
-- the backfill so existing numbers are never re-issued.
CREATE TABLE course_number_seq (
    id   INTEGER PRIMARY KEY CHECK (id = 1),
    next INTEGER NOT NULL
);
INSERT INTO course_number_seq (id, next)
SELECT 1, COALESCE(MAX(number), 0) + 1 FROM courses;
