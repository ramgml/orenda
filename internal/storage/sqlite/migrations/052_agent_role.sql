-- Migration 052 — agent role (master-agent-role plan, step 1).
--
-- Agents gain a `role` distinguishing project-scoped workers from master
-- agents. Values are the agent.Role literals: 'project' (default) and
-- 'master'. A master agent is the owner-equivalent on the user namespace
-- (internal/api RequireUser Bearer fallback); every action it performs is
-- attributed to the agent (actor_type='agent'), never to the owner user.
--
-- Existing rows backfill to 'project' via NOT NULL DEFAULT — the pre-052
-- behaviour for every agent is exactly project-scoped. The column is not
-- part of any index (agents indexes cover status/last_seen only), so the
-- down-side plain DROP COLUMN is safe.

ALTER TABLE agents ADD COLUMN role TEXT NOT NULL DEFAULT 'project';
