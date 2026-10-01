-- ============================================================================
-- 004_agent_role.down.sql — drop agents.role
-- ============================================================================
-- Destructive by design: role='master' agents collapse back to plain
-- project agents; the master bearer fallback in RequireUser stops
-- matching.

ALTER TABLE agents DROP COLUMN role;
