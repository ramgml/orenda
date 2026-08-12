-- ============================================================================
-- 019_courses.down.sql — drop LMS tables
-- ============================================================================
-- Phase 18 created the courses / modules / lessons / quizzes
-- hierarchy. CASCADE on courses kills modules, lessons, and
-- quizzes; we drop explicitly so the order is obvious and the
-- indexes go first.
--
-- orenda:foreign_keys_off
-- courses has a FK from generator_task_id to tasks.id; with FK
-- enforcement on, the DROP TABLE fails because tasks still
-- reference the (soon-to-be-dropped) course_lessons via task_id.
-- We must run with FK off so the cascade succeeds without
-- complaints.

DROP INDEX IF EXISTS idx_course_quizzes_lesson;
DROP INDEX IF EXISTS idx_course_lessons_module;
DROP INDEX IF EXISTS idx_course_modules_course;
DROP INDEX IF EXISTS idx_courses_status;
DROP INDEX IF EXISTS idx_courses_owner;

DROP TABLE IF EXISTS course_quizzes;
DROP TABLE IF EXISTS course_lessons;
DROP TABLE IF EXISTS course_modules;
DROP TABLE IF EXISTS courses;
