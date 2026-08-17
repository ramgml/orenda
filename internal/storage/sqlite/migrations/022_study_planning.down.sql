-- ============================================================================
-- 022_study_planning.down.sql — Phase 31 reverse
-- ============================================================================
-- Destructive by design: we cannot restore the column values that existed
-- before 022 was applied. Down is intended for the round-trip tests on
-- synthetic fixtures; on a production instance that has been live long
-- enough for tutors to write pace_notes_md or agents to file proposals,
-- treat down as data loss.
--
-- `DROP COLUMN` requires SQLite ≥ 3.35. modernc.org/sqlite bundles
-- 3.45+; we have headroom.
--
-- The order matters: drop dependent indexes/tables first, then the
-- columns they reference, then the column on courses.

DROP INDEX IF EXISTS idx_study_proposals_status_created;
DROP TABLE IF EXISTS study_proposals;

DROP INDEX IF EXISTS idx_tasks_study_course;
ALTER TABLE tasks   DROP COLUMN study_course_id;
ALTER TABLE courses DROP COLUMN pace_notes_md;