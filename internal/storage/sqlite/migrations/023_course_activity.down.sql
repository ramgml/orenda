-- Rollback for migration 023.

DROP INDEX IF EXISTS idx_course_activity_actor;
DROP INDEX IF EXISTS idx_course_activity_course;
DROP TABLE IF EXISTS course_activity;