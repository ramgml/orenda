-- ============================================================================
-- 004_agent_role.up.sql — agents.role column (master-agent-role plan)
-- ============================================================================
-- Postgres counterpart of the sqlite migration 052_agent_role.sql; the two
-- stay in lockstep because the storage seam runs the same Go repos on both
-- dialects.
--
-- Values are the agent.Role literals: 'project' (default) and 'master'.
-- A master agent is the owner-equivalent on the user namespace
-- (internal/api RequireUser Bearer fallback); every action it performs is
-- attributed to the agent (actor_type='agent'), never to the owner user.
-- Existing rows backfill to 'project' via NOT NULL DEFAULT.

ALTER TABLE agents ADD COLUMN role TEXT NOT NULL DEFAULT 'project';
