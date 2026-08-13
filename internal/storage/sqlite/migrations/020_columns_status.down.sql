-- Phase 27.8 (reverse): drop the columns.status machine key and its
-- unique index. Existing data loses the canonical mapping — Phase 27.7
-- accepts an empty status so callers fall back to the existing
-- loose coupling.

DROP INDEX IF EXISTS idx_columns_board_status;
ALTER TABLE columns DROP COLUMN status;
