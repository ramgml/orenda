-- ============================================================================
-- 019_courses.sql — LMS tables (Phase 18)
-- ============================================================================
-- A personal learning system driven by an external AI-агент-тьютор.
-- The user states an intent ("learn Rust in a month, 3x/week");
-- the tutor builds a curriculum (modules + lessons), the user
-- approves, then lessons open sequentially until the course is done.
--
-- Schema overview:
--
--   courses           — the top-level entity. status (draft|review|active|
--                       done|archived) tracks the lifecycle.
--   course_modules    — ordered groups of lessons inside a course.
--   course_lessons    — individual learning units. status (locked|open|
--                       done) controls sequential unlocking. links to an
--                       optional task (the exercise / practice work).
--   course_quizzes    — questions under a lesson. `kind` (open|exact)
--                       drives automatic vs manual grading.
--
-- Lifecycle: draft → review (tutor submitted) → active (user accepted)
--            → done (all lessons completed). Plus archived (terminal).
--
-- Notes:
--   - owner_id FK to users: single-owner today, but the column is here
--     so the future multi-user story doesn't need a migration.
--   - generator_task_id FK to tasks: every course has a "build the
--     curriculum" task that the tutor agent picks up via the existing
--     agent work-queue (Phase 3). When the tutor submits a curriculum,
--     the task is marked done; the course moves to review.
--   - task_id FK on lessons: the exercise backing the lesson. SET NULL
--     on delete because a course row can outlive a transient task.
--   - All FKs use ON DELETE CASCADE where the row is meaningless without
--     its parent (modules, lessons, quizzes); SET NULL where the task
--     outlives the lesson (e.g. user progresses past the lesson before
--     the exercise gets archived).

CREATE TABLE courses (
    id              TEXT PRIMARY KEY,
    title           TEXT NOT NULL,
    intent_md       TEXT NOT NULL DEFAULT '',
    level           TEXT NOT NULL DEFAULT 'beginner',  -- beginner|intermediate|advanced
    pace            TEXT NOT NULL DEFAULT 'casual',    -- casual|regular|intensive
    status          TEXT NOT NULL DEFAULT 'draft',     -- draft|review|active|done|archived
    owner_id        TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    generator_task_id TEXT REFERENCES tasks(id) ON DELETE SET NULL,
    created_at      TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at      TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX idx_courses_owner   ON courses(owner_id);
CREATE INDEX idx_courses_status  ON courses(status);

CREATE TABLE course_modules (
    id              TEXT PRIMARY KEY,
    course_id       TEXT NOT NULL REFERENCES courses(id) ON DELETE CASCADE,
    title           TEXT NOT NULL,
    description     TEXT NOT NULL DEFAULT '',
    position        INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX idx_course_modules_course ON course_modules(course_id, position);

CREATE TABLE course_lessons (
    id              TEXT PRIMARY KEY,
    module_id       TEXT NOT NULL REFERENCES course_modules(id) ON DELETE CASCADE,
    title           TEXT NOT NULL,
    content_md      TEXT NOT NULL DEFAULT '',
    status          TEXT NOT NULL DEFAULT 'locked',    -- locked|open|done
    position        INTEGER NOT NULL DEFAULT 0,
    task_id         TEXT REFERENCES tasks(id) ON DELETE SET NULL
);
CREATE INDEX idx_course_lessons_module ON course_lessons(module_id, position);

CREATE TABLE course_quizzes (
    id              TEXT PRIMARY KEY,
    lesson_id       TEXT NOT NULL REFERENCES course_lessons(id) ON DELETE CASCADE,
    position        INTEGER NOT NULL DEFAULT 0,
    question_md     TEXT NOT NULL,
    expected_md     TEXT NOT NULL DEFAULT '',
    kind            TEXT NOT NULL DEFAULT 'open'      -- open|exact
);
CREATE INDEX idx_course_quizzes_lesson ON course_quizzes(lesson_id, position);
