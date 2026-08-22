-- 025_task_retracted.down.sql — reverse the retract tombstones.
--
-- The tombstones are append-only audit data; deleting them is a
-- real audit-loss event. The migration runner should still drop the
-- table when MigrateDown reaches this point (rare in production).
DROP TABLE IF EXISTS task_retracted;
