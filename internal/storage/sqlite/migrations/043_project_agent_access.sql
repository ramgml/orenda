-- ============================================================================
-- 043_project_agent_access.sql — per-project agent access scope (task 140)
-- ============================================================================
-- Wiki: agent-project-scope. Before this migration every agent could see
-- and claim every project's tasks in the dogfood instance — an orchestrator
-- could pick up tasks belonging to a different project than the one it was
-- dispatched for (the T140 incident). This migration introduces an explicit
-- allow-list model:
--
--   * projects.agents_allowed INTEGER NOT NULL DEFAULT 0 — the project-wide
--     switch. 0 (the new default, applied to fresh projects automatically)
--     means "closed: only explicitly granted agents". 1 means "open to all
--     agents".
--   * project_agents — the grant list for closed projects:
--     (project_id, agent_id) with FKs ON DELETE CASCADE both ways, so
--     deleting a project or an agent cleans the grant rows without orphans.
--     added_by records WHICH user made the grant (audit, single-owner
--     today but the column costs nothing), added_at defaults to now.
--   * idx_project_agents_agent — answers the agent-side question "which
--     projects am I granted?" (AgentAccessibleProjectIDs) without a full
--     scan; the PK already covers the per-project direction.
--
-- Semantics: access = agents_allowed = 1 OR a project_agents row exists.
-- An empty grant list on a closed project means NO agent has access —
-- closing a project is a two-step owner action (flip the flag, grant
-- explicitly), never a silent default.
--
-- Down: drop the index/table and the column (schema is fully reversible;
-- grant history is intentionally lost with the table).

ALTER TABLE projects ADD COLUMN agents_allowed INTEGER NOT NULL DEFAULT 0;

CREATE TABLE project_agents (
    project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    agent_id   TEXT NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
    added_by   TEXT NOT NULL REFERENCES users(id),
    added_at   TEXT NOT NULL DEFAULT (datetime('now')),
    PRIMARY KEY (project_id, agent_id)
);

CREATE INDEX idx_project_agents_agent ON project_agents(agent_id);
