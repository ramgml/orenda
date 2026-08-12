-- ============================================================================
-- 011_sync_ops.down.sql — drop sync_ops table + index
-- ============================================================================

DROP INDEX IF EXISTS idx_sync_ops_target;
DROP TABLE IF EXISTS sync_ops;
