-- ============================================================================
-- 043_project_agent_access.down.sql — revert per-project agent access scope
-- ============================================================================
-- Order matters: the index and the grant table go first, then the column
-- ALTER TABLE projects ADD COLUMN introduced in the up migration.

DROP INDEX idx_project_agents_agent;
DROP TABLE project_agents;
ALTER TABLE projects DROP COLUMN agents_allowed;
