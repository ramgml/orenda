-- Rollback for migration 024.

DROP INDEX IF EXISTS idx_project_activity_actor;
DROP INDEX IF EXISTS idx_project_activity_project;
DROP TABLE IF EXISTS project_activity;
