-- ============================================================================
-- 011_sync_ops.sql — offline-sync idempotency table
-- ============================================================================
-- Phase 8, task 8.5.
--
-- Records the client_id → server_id mapping for operations applied via
-- POST /api/v1/sync. The PK on client_id makes a retry of the same op
-- a no-op from the server's perspective (the handler returns the existing
-- id instead of creating a duplicate row).

CREATE TABLE IF NOT EXISTS sync_ops (
    client_id   TEXT PRIMARY KEY,           -- PWA-side idempotency key
    server_id   TEXT NOT NULL,              -- the row created for this op
    op          TEXT NOT NULL,              -- create_task | update_task | ...
    target      TEXT NOT NULL,
    applied_at  TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX IF NOT EXISTS idx_sync_ops_target ON sync_ops(target);