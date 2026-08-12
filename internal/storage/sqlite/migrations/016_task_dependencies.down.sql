-- ============================================================================
-- 016_task_dependencies.down.sql — drop the dependency graph
-- ============================================================================
-- Phase 15 stored every "X blocks Y" relation. Rolling back wipes
-- the rows (CASCADE on tasks would do it anyway). The service's
-- Claim check will start letting agents claim blocked tasks again.

DROP INDEX IF EXISTS idx_task_deps_depends_on;
DROP INDEX IF EXISTS idx_task_deps_task;
DROP TABLE IF EXISTS task_dependencies;
