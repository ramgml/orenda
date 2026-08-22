-- ============================================================================
-- 039_lesson_numbers.sql — human-readable sequential lesson numbers
-- ============================================================================
-- Every lesson gets a small monotonically-increasing integer (`number`)
-- alongside its UUID. Lessons are numbered GLOBALLY (not per-course) so
-- "L10" unambiguously identifies a lesson across all courses.
--
-- Assignment rule for NEW rows: courseRepo.CreateLesson draws the next
-- number from the `lesson_number_seq` high-watermark (UPDATE ... RETURNING
-- in the same transaction as the INSERT). A bare COALESCE(MAX(number),0)+1
-- over `lessons` would REUSE a number after the newest lesson is deleted —
-- an "L10" reference has to keep pointing at the same lesson forever, so
-- the watermark never moves backwards. Numbers are NEVER reused after a
-- delete.
--
-- Why the three-step shape for the column: same rationale as 036 and 038 —
-- SQLite's ALTER TABLE ... ADD COLUMN ... NOT NULL requires a CONSTANT
-- default, so the column lands as DEFAULT 0; the backfill rewrites in
-- (created_at, rowid) order via ROW_NUMBER(); the UNIQUE index is created
-- only after the backfill. One transaction, no half-backfilled state visible.

ALTER TABLE course_lessons ADD COLUMN number INTEGER NOT NULL DEFAULT 0;

-- course_lessons has no created_at column, so we order by rowid only
-- (stable insertion order within a single migration run).
UPDATE course_lessons SET number = (
    SELECT numbered.rn FROM (
        SELECT id, ROW_NUMBER() OVER (ORDER BY rowid) AS rn
        FROM course_lessons
    ) AS numbered
    WHERE numbered.id = course_lessons.id
);

CREATE UNIQUE INDEX idx_lessons_number ON course_lessons(number);

-- High-watermark for new assignments. One row, id pinned to 1;
-- `next` is the number the following Create will draw. Seeded past
-- the backfill so existing numbers are never re-issued.
CREATE TABLE lesson_number_seq (
    id   INTEGER PRIMARY KEY CHECK (id = 1),
    next INTEGER NOT NULL
);
INSERT INTO lesson_number_seq (id, next)
SELECT 1, COALESCE(MAX(number), 0) + 1 FROM course_lessons;
