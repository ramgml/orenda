-- ============================================================================
-- 003_rejected_column.up.sql — canonical `rejected` parking column
-- ============================================================================
-- Task 376 (PRD F-T-3, P0): backlog → todo → in_progress → review → done
-- with a rejected → todo loop. Postgres counterpart of the sqlite
-- migration 051_rejected_column.sql; the two stay in lockstep because the
-- storage seam runs the same Go repos on both dialects.
--
-- One `rejected` column (name = machine key, like the other defaults) per
-- board, appended AFTER `done` (position = max(existing) + 1024). Both are
-- terminal parking — done the completion archive, rejected the
-- declined-work archive — so the active pipeline stays contiguous and
-- append-only positioning shifts no existing card (project DefaultColumns
-- uses the same order). color '#ef4444' is the signature red every render
-- path uses for the parking column (matches project.RejectedColumnColor).
--
-- No task backfill: existing rows keep their column/status. Rejected is
-- reached by an explicit human move, never assigned retroactively.
--
-- Idempotent by construction: boards that already carry a column with the
-- machine key `rejected` (a Phase 27.8 custom column) are skipped — the
-- UNIQUE index idx_columns_board_status (baseline) would reject a
-- duplicate anyway, but the NOT EXISTS guard keeps a manual re-run a
-- clean no-op.

INSERT INTO columns (id, board_id, name, position, wip_limit, color, status)
SELECT
    -- App-generated TEXT ids everywhere else; a migration-generated row
    -- only needs global uniqueness, which md5 of the board id + clock
    -- provides (one row per board, no collisions within the run).
    'col-' || md5(b.id || ':' || clock_timestamp()::text || ':' || random()::text),
    b.id,
    'rejected',
    COALESCE((SELECT MAX(c.position) FROM columns c WHERE c.board_id = b.id), 0) + 1024,
    NULL,
    '#ef4444',
    'rejected'
FROM boards b
WHERE NOT EXISTS (
    SELECT 1 FROM columns c WHERE c.board_id = b.id AND c.status = 'rejected'
);
