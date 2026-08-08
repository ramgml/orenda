-- ============================================================================
-- 001_init.sql — initial Orenda schema
-- ============================================================================
-- Single binary, single owner; agents and bots are first-class via api_tokens.
-- All timestamps are ISO-8601 strings (datetime('now') in SQLite).
-- All IDs are UUIDv7 strings.

PRAGMA foreign_keys = ON;

-- ----------------------------------------------------------------------------
-- Users (single owner + future agents reference users via api_tokens)
-- ----------------------------------------------------------------------------
CREATE TABLE users (
    id              TEXT PRIMARY KEY,             -- UUIDv7
    email           TEXT NOT NULL UNIQUE,
    password_hash   TEXT NOT NULL,                -- bcrypt
    display_name    TEXT NOT NULL,
    role            TEXT NOT NULL DEFAULT 'owner', -- owner
    created_at      TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at      TEXT NOT NULL DEFAULT (datetime('now'))
);

-- ----------------------------------------------------------------------------
-- API tokens (for agents and CLI; bcrypt-hashed)
-- ----------------------------------------------------------------------------
CREATE TABLE api_tokens (
    id              TEXT PRIMARY KEY,
    user_id         TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name            TEXT NOT NULL,
    hash            TEXT NOT NULL,                -- bcrypt of opaque token
    scopes          TEXT NOT NULL,                -- JSON array
    last_used_at    TEXT,
    expires_at      TEXT,
    created_at      TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX idx_api_tokens_user ON api_tokens(user_id);

-- ----------------------------------------------------------------------------
-- Agents
-- ----------------------------------------------------------------------------
CREATE TABLE agents (
    id              TEXT PRIMARY KEY,
    name            TEXT NOT NULL UNIQUE,
    type            TEXT NOT NULL,                -- 'qwen' | 'claude' | 'custom'
    description     TEXT,
    token_id        TEXT NOT NULL REFERENCES api_tokens(id) ON DELETE CASCADE,
    last_seen_at    TEXT,
    status          TEXT NOT NULL DEFAULT 'offline',  -- online | offline | disabled
    max_concurrent  INTEGER NOT NULL DEFAULT 3,
    created_at      TEXT NOT NULL DEFAULT (datetime('now'))
);

-- ----------------------------------------------------------------------------
-- Projects, Boards, Columns
-- ----------------------------------------------------------------------------
CREATE TABLE projects (
    id              TEXT PRIMARY KEY,
    name            TEXT NOT NULL,
    color           TEXT NOT NULL DEFAULT '#3b82f6',
    description     TEXT,
    owner_id        TEXT NOT NULL REFERENCES users(id),
    archived        INTEGER NOT NULL DEFAULT 0,
    created_at      TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at      TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX idx_projects_owner ON projects(owner_id);

CREATE TABLE boards (
    id              TEXT PRIMARY KEY,
    project_id      TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    name            TEXT NOT NULL DEFAULT 'Main',
    position        INTEGER NOT NULL DEFAULT 0,
    created_at      TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX idx_boards_project ON boards(project_id);

CREATE TABLE columns (
    id              TEXT PRIMARY KEY,
    board_id        TEXT NOT NULL REFERENCES boards(id) ON DELETE CASCADE,
    name            TEXT NOT NULL,                -- backlog | todo | in_progress | review | done
    position        REAL NOT NULL,                -- float for drag-and-drop ordering
    wip_limit       INTEGER,
    color           TEXT
);
CREATE INDEX idx_columns_board ON columns(board_id);

-- ----------------------------------------------------------------------------
-- Tags
-- ----------------------------------------------------------------------------
CREATE TABLE tags (
    id              TEXT PRIMARY KEY,
    name            TEXT NOT NULL UNIQUE,
    color           TEXT
);

-- ----------------------------------------------------------------------------
-- Tasks
-- ----------------------------------------------------------------------------
CREATE TABLE tasks (
    id              TEXT PRIMARY KEY,
    project_id      TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    parent_task_id  TEXT REFERENCES tasks(id) ON DELETE CASCADE,
    column_id       TEXT REFERENCES columns(id) ON DELETE SET NULL,
    title           TEXT NOT NULL,
    description     TEXT,
    status          TEXT NOT NULL DEFAULT 'todo',
    priority        TEXT NOT NULL DEFAULT 'medium',  -- low | medium | high | urgent
    assignee_type   TEXT,                           -- user | agent | NULL
    assignee_id     TEXT,                           -- user.id or agent.id
    awaiting        TEXT NOT NULL DEFAULT 'none',   -- none | human | agent
    context_md      TEXT,
    agent_notes     TEXT,
    due_at          TEXT,
    started_at      TEXT,
    claimed_at      TEXT,
    completed_at    TEXT,
    time_estimate_s INTEGER,
    time_spent_s    INTEGER NOT NULL DEFAULT 0,
    position        REAL NOT NULL DEFAULT 0,
    created_at      TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at      TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX idx_tasks_project ON tasks(project_id);
CREATE INDEX idx_tasks_status ON tasks(status);
CREATE INDEX idx_tasks_assignee ON tasks(assignee_type, assignee_id);
CREATE INDEX idx_tasks_due ON tasks(due_at);
CREATE INDEX idx_tasks_parent ON tasks(parent_task_id);

-- ----------------------------------------------------------------------------
-- Task locks (atomic claim by an agent)
-- ----------------------------------------------------------------------------
CREATE TABLE task_locks (
    task_id         TEXT PRIMARY KEY REFERENCES tasks(id) ON DELETE CASCADE,
    agent_id        TEXT NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
    acquired_at     TEXT NOT NULL DEFAULT (datetime('now'))
);

-- ----------------------------------------------------------------------------
-- Subtasks and checklists
-- ----------------------------------------------------------------------------
CREATE TABLE subtasks (
    id              TEXT PRIMARY KEY,
    task_id         TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    title           TEXT NOT NULL,
    done            INTEGER NOT NULL DEFAULT 0,
    position        INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX idx_subtasks_task ON subtasks(task_id);

CREATE TABLE checklists (
    id              TEXT PRIMARY KEY,
    task_id         TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    title           TEXT NOT NULL,
    position        INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX idx_checklists_task ON checklists(task_id);

CREATE TABLE checklist_items (
    id              TEXT PRIMARY KEY,
    checklist_id    TEXT NOT NULL REFERENCES checklists(id) ON DELETE CASCADE,
    title           TEXT NOT NULL,
    done            INTEGER NOT NULL DEFAULT 0,
    position        INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE task_tags (
    task_id         TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    tag_id          TEXT NOT NULL REFERENCES tags(id) ON DELETE CASCADE,
    PRIMARY KEY (task_id, tag_id)
);

-- ----------------------------------------------------------------------------
-- Comments, mentions, attachments, activity
-- ----------------------------------------------------------------------------
CREATE TABLE comments (
    id              TEXT PRIMARY KEY,
    target_type     TEXT NOT NULL,                 -- task | page | event
    target_id       TEXT NOT NULL,
    author_type     TEXT NOT NULL,                 -- user | agent
    author_id       TEXT NOT NULL,
    body_md         TEXT NOT NULL,
    created_at      TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX idx_comments_target ON comments(target_type, target_id);

CREATE TABLE mentions (
    comment_id      TEXT NOT NULL REFERENCES comments(id) ON DELETE CASCADE,
    target_type     TEXT NOT NULL,                 -- user | agent
    target_id       TEXT NOT NULL,
    PRIMARY KEY (comment_id, target_type, target_id)
);

CREATE TABLE attachments (
    id              TEXT PRIMARY KEY,
    target_type     TEXT NOT NULL,
    target_id       TEXT NOT NULL,
    filename        TEXT NOT NULL,
    mime            TEXT NOT NULL,
    size            INTEGER NOT NULL,
    path            TEXT NOT NULL,
    sha256          TEXT NOT NULL,
    uploaded_by_type TEXT NOT NULL,
    uploaded_by_id  TEXT NOT NULL,
    created_at      TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX idx_attachments_target ON attachments(target_type, target_id);

CREATE TABLE task_activity (
    id              TEXT PRIMARY KEY,
    task_id         TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    actor_type      TEXT NOT NULL,
    actor_id        TEXT NOT NULL,
    action          TEXT NOT NULL,                 -- created | claimed | moved | commented | status_changed | ...
    payload         TEXT,
    created_at      TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX idx_activity_task ON task_activity(task_id, created_at);

-- ----------------------------------------------------------------------------
-- Time entries
-- ----------------------------------------------------------------------------
CREATE TABLE time_entries (
    id              TEXT PRIMARY KEY,
    task_id         TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    agent_id        TEXT REFERENCES agents(id) ON DELETE SET NULL,
    started_at      TEXT NOT NULL,
    ended_at        TEXT,
    duration_s      INTEGER,
    source          TEXT NOT NULL DEFAULT 'manual' -- timer | manual
);
CREATE INDEX idx_time_task ON time_entries(task_id);

-- ----------------------------------------------------------------------------
-- Calendar events
-- ----------------------------------------------------------------------------
CREATE TABLE events (
    id              TEXT PRIMARY KEY,
    title           TEXT NOT NULL,
    description     TEXT,
    start_at        TEXT NOT NULL,
    end_at          TEXT NOT NULL,
    all_day         INTEGER NOT NULL DEFAULT 0,
    color           TEXT,
    project_id      TEXT REFERENCES projects(id) ON DELETE SET NULL,
    recurrence_rule TEXT,
    parent_event_id TEXT REFERENCES events(id) ON DELETE SET NULL,
    created_at      TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at      TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX idx_events_start ON events(start_at);

-- ----------------------------------------------------------------------------
-- Wiki pages and links
-- ----------------------------------------------------------------------------
CREATE TABLE wiki_pages (
    id              TEXT PRIMARY KEY,
    parent_id       TEXT REFERENCES wiki_pages(id) ON DELETE CASCADE,
    slug            TEXT NOT NULL UNIQUE,
    title           TEXT NOT NULL,
    content_md      TEXT,
    position        INTEGER NOT NULL DEFAULT 0,
    created_at      TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at      TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX idx_pages_parent ON wiki_pages(parent_id);

CREATE TABLE wiki_links (
    from_page_id    TEXT NOT NULL REFERENCES wiki_pages(id) ON DELETE CASCADE,
    to_page_id      TEXT NOT NULL REFERENCES wiki_pages(id) ON DELETE CASCADE,
    PRIMARY KEY (from_page_id, to_page_id)
);

-- ----------------------------------------------------------------------------
-- Notifications and bot subscriptions
-- ----------------------------------------------------------------------------
CREATE TABLE notifications (
    id              TEXT PRIMARY KEY,
    user_id         TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    type            TEXT NOT NULL,
    target_type     TEXT,
    target_id       TEXT,
    payload         TEXT,
    read_at         TEXT,
    dedup_key       TEXT NOT NULL UNIQUE,
    created_at      TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX idx_notif_user ON notifications(user_id, read_at);

CREATE TABLE bot_subscriptions (
    id              TEXT PRIMARY KEY,
    user_id         TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    bot_type        TEXT NOT NULL,                 -- console | vk | telegram | email | webhook
    target_address  TEXT NOT NULL,
    events          TEXT NOT NULL,                 -- JSON array of event types
    enabled         INTEGER NOT NULL DEFAULT 1,
    created_at      TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX idx_subs_user ON bot_subscriptions(user_id);

-- ----------------------------------------------------------------------------
-- Backup settings and log
-- ----------------------------------------------------------------------------
CREATE TABLE backup_settings (
    key             TEXT PRIMARY KEY,
    value           TEXT NOT NULL                  -- JSON
);

CREATE TABLE backup_log (
    id              TEXT PRIMARY KEY,
    type            TEXT NOT NULL,                 -- git_push | sqlite_snapshot | wal_archive
    status          TEXT NOT NULL,                 -- success | failed
    message         TEXT,
    snapshot_path   TEXT,
    created_at      TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX idx_backup_log_created ON backup_log(created_at);

-- ----------------------------------------------------------------------------
-- FTS5 virtual tables (created in Phase 5)
-- ----------------------------------------------------------------------------
-- CREATE VIRTUAL TABLE tasks_fts   USING fts5(title, description, content='tasks',        content_rowid='rowid');
-- CREATE VIRTUAL TABLE pages_fts   USING fts5(title, content_md, content='wiki_pages',   content_rowid='rowid');
-- CREATE VIRTUAL TABLE comments_fts USING fts5(body_md,             content='comments',    content_rowid='rowid');