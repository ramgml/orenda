-- Migration 052 (down) — drop the agent `role` column.
--
-- Destructive by design: role=master agents collapse back to plain
-- project agents; the master bearer fallback in RequireUser stops
-- matching. Column is outside every index, so a plain DROP COLUMN is
-- safe on all supported sqlite versions (precedent:
-- 024_task_created_by.down.sql).

ALTER TABLE agents DROP COLUMN role;
