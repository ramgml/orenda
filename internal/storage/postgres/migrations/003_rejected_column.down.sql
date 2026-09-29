-- ============================================================================
-- 003_rejected_column.down.sql — drop the canonical `rejected` columns
-- ============================================================================
-- Task 376 reverse. Best-effort: deletes the columns the up-side would
-- have inserted (name = machine key = 'rejected'). A pre-003 Phase 27.8
-- custom column that was later RENAMED survives (name no longer matches);
-- one still named 'rejected' is deleted too — the up-side NOT EXISTS
-- guard cannot distinguish it from ours. Tasks are untouched.

DELETE FROM columns WHERE status = 'rejected' AND name = 'rejected';
