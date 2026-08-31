-- Rollback for migration 041 (Task 112).
--
-- SQLite ALTER TABLE DROP COLUMN requires 3.35.0+ (Jan 2021);
-- modernc.org/sqlite bundles a recent version so this is safe
-- (same pattern as the 035 rollback).

ALTER TABLE comments DROP COLUMN edited_at;
