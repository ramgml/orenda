-- Rollback for migration 042 (Task 115).
--
-- SQLite ALTER TABLE DROP COLUMN requires 3.35.0+ (Jan 2021);
-- modernc.org/sqlite bundles a recent version so this is safe
-- (same pattern as the 041 / 035 rollbacks).
--
-- Note: `status` values of 'blocked' written by the app are NOT
-- rewritten here — statuses are app-layer enforced (no CHECK
-- constraint on the column). Operators rolling back a build that
-- understands `blocked` should migrate such rows by hand if any exist.

ALTER TABLE tasks DROP COLUMN blocked_prev_status;
