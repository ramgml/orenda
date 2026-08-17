-- ============================================================================
-- 022_study_planning.sql — Study reminders & opt-in proposals (Phase 31)
-- ============================================================================
-- A study reminder is an inbox task whose `study_course_id` points at the
-- course the reminder is about. The link is loose: deleting the course
-- clears the link on remaining tasks (SET NULL) but the task itself
-- survives — old reminders don't disappear when a course is archived or
-- removed. Today groups them under `due_today` rather than `overdue`, so
-- a missed day never turns red; that policy lives in /today, not in
-- scheduled mutations.
--
-- `study_proposals` is the opt-in tray: an external agent (opencode via
-- MCP) writes a list of "what to study today" proposals; the user accepts
-- each one explicitly through a Dashboard tray. Accept materialises a
-- real inbox task (non-null study_course_id, due_at = max(target_date,
-- today)); dismiss just marks the proposal resolved. Accept is
-- idempotent — repeating the call returns the previously-created task
-- instead of creating a duplicate.
--
-- Additive only: no rebuild of `tasks` (migration 015 already paid that
-- cost), no FK changes to existing tables. New columns are nullable /
-- have defaults; existing rows are unaffected.
--
-- FK semantics summary:
--   courses.pace_notes_md        — new column, default '' (no FK)
--   tasks.study_course_id        — ON DELETE SET NULL (course gone → reminder survives)
--   study_proposals.course_id    — ON DELETE CASCADE (course gone → proposals go too)
--   study_proposals.created_by_agent
--                                — no ON DELETE clause (default NO ACTION;
--                                  protects audit trail — operator must
--                                  clear proposals before removing an agent)
--   study_proposals.accepted_task_id
--                                — ON DELETE SET NULL (task gone → proposal
--                                  still records the original accept event)

ALTER TABLE courses ADD COLUMN pace_notes_md TEXT NOT NULL DEFAULT '';
ALTER TABLE tasks   ADD COLUMN study_course_id TEXT REFERENCES courses(id) ON DELETE SET NULL;

-- Partial index: most tasks have no study_course_id. A full index would
-- be wasted space; the WHERE clause keeps the index small and the
-- /today filtering cheap.
CREATE INDEX idx_tasks_study_course
    ON tasks(study_course_id)
    WHERE study_course_id IS NOT NULL;

CREATE TABLE study_proposals (
    id                  TEXT PRIMARY KEY,
    course_id           TEXT REFERENCES courses(id) ON DELETE CASCADE,
    title               TEXT NOT NULL,
    body_md             TEXT NOT NULL DEFAULT '',
    target_date         TEXT NOT NULL,                       -- YYYY-MM-DD (per domain.Validate)
    status              TEXT NOT NULL DEFAULT 'pending'
                            CHECK (status IN ('pending','accepted','dismissed')),
    created_by_agent    TEXT NOT NULL REFERENCES agents(id),
    accepted_task_id    TEXT REFERENCES tasks(id) ON DELETE SET NULL,
    created_at          TEXT NOT NULL DEFAULT (datetime('now')),
    resolved_at         TEXT
);

-- Tray query: list pending proposals in created_at order.
CREATE INDEX idx_study_proposals_status_created
    ON study_proposals(status, created_at);