-- ============================================================================
-- 033_task_numbers.down.sql — drop the human-readable task number
-- ============================================================================
-- Reversible in schema terms but lossy in data terms: the number
-- assignments are gone after the down (re-applying the up backfills
-- afresh — row order is stable because created_at/rowid don't move,
-- but anything referencing a number externally can't rely on that
-- after a down/up round-trip on a live instance).
--
-- `DROP COLUMN` requires SQLite ≥ 3.35. modernc.org/sqlite bundles
-- 3.45+; we have headroom (same convention as 022_study_planning).
-- Dependents go first: the unique index, the high-watermark table,
-- then the column itself.

DROP INDEX IF EXISTS idx_tasks_number;
DROP TABLE IF EXISTS task_number_seq;
ALTER TABLE tasks DROP COLUMN number;
