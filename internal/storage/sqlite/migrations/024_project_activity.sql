-- Migration 024 — Project activity feed (wiki:agent-project-description).
--
-- Before this, project.description was editable only through the
-- user-cookie PATCH (handlers_projects.go) and wrote no audit row.
-- task_activity only covered tasks; course_activity (migration 023)
-- covered LMS objects. There was no surface that could record "agent
-- harness-mcp updated project X description" with a before/after diff.
--
-- This mirrors the course_activity shape (migration 023) but scoped
-- to projects. Read side is reserved for a future v1.x phase; this
-- migration ships the table so the writer side can be wired without
-- a follow-up migration. Kind values are closed-set strings; new ones
-- land in alphabetical order at the end so existing rows still parse.

CREATE TABLE project_activity (
    id              TEXT PRIMARY KEY,
    project_id      TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    actor_type      TEXT NOT NULL,  -- user | agent
    actor_id        TEXT NOT NULL,
    kind            TEXT NOT NULL,  -- description_changed
    payload         TEXT,           -- free-form small JSON, optional
    created_at      TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX idx_project_activity_project ON project_activity(project_id, created_at);
CREATE INDEX idx_project_activity_actor   ON project_activity(actor_id, created_at);
