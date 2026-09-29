-- Migration 051 — canonical `rejected` column (Task 376, PRD F-T-3).
--
-- PRD F-T-3 (P0): backlog → todo → in_progress → review → done with a
-- rejected → todo loop. The status existed in the app layer only as a
-- Phase 27.8 machine-key possibility; this migration makes it canonical
-- on every board:
--
-- - One `rejected` column (name = machine key, like the other defaults)
--   per board, appended AFTER `done` (position = max(existing) + 1024).
--   Both are terminal parking — done the completion archive, rejected
--   the declined-work archive — so the active pipeline stays contiguous
--   and append-only positioning shifts no existing card (project
--   DefaultColumns uses the same order).
-- - color '#ef4444' — the signature red every render path uses for the
--   parking column (matches project.RejectedColumnColor in Go).
-- - No task backfill: existing rows keep their column/status. Rejected
--   is reached by an explicit human move, never assigned retroactively.
--
-- Idempotent by construction: boards that already carry a column with
-- the machine key `rejected` (a Phase 27.8 custom column) are skipped —
-- the UNIQUE(board_id, status) index from 020 would reject a duplicate
-- anyway, but the NOT EXISTS guard keeps a manual re-run a clean no-op.

INSERT INTO columns (id, board_id, name, position, wip_limit, color, status)
SELECT
    -- v4 UUID (lowercase hex, 8-4-4-4-12 layout) — same generator as 013.
    lower(
        hex(randomblob(4)) || '-' ||
        hex(randomblob(2)) || '-' ||
        hex(randomblob(2)) || '-' ||
        hex(randomblob(2)) || '-' ||
        hex(randomblob(6))
    ),
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
