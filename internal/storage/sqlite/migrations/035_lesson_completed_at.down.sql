-- Rollback for migration 035.
--
-- SQLite ALTER TABLE DROP COLUMN requires 3.35.0+ (Jan 2021);
-- modernc.org/sqlite bundles a recent version so this is safe.

DROP INDEX IF EXISTS idx_course_lessons_completed;
ALTER TABLE course_lessons DROP COLUMN completed_at;