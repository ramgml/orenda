-- Migration 023 — Course activity feed (Phase 32.5 pilot task #2).
--
-- Before this, course mutations (create, approve, agent activate,
-- granular curriculum CRUD, lesson/quiz edit, status change) wrote
-- no audit row. task_activity only covered tasks. Operators couldn't
-- reconstruct who changed what when for a course.
--
-- This mirrors task_activity's shape (id, target_id, actor, action,
-- payload, timestamp) but for courses. Read side is
-- GET /api/v1/courses/{id}/activity newest-first.

CREATE TABLE course_activity (
    id              TEXT PRIMARY KEY,
    course_id       TEXT NOT NULL REFERENCES courses(id) ON DELETE CASCADE,
    actor_type      TEXT NOT NULL,  -- user | agent
    actor_id        TEXT NOT NULL,
    kind            TEXT NOT NULL,  -- created | approved | activated | curriculum_swapped
                                    -- lesson_added | lesson_removed | lesson_edited
                                    -- quiz_added | quiz_removed | quiz_edited
                                    -- module_added | module_removed | module_edited
                                    -- status_changed | archived
    payload         TEXT,           -- free-form small JSON-ish, optional
    created_at      TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX idx_course_activity_course ON course_activity(course_id, created_at);
CREATE INDEX idx_course_activity_actor  ON course_activity(actor_id, created_at);