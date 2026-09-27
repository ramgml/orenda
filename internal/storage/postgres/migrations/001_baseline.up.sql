-- ============================================================================
-- 001_baseline.up.sql — PostgreSQL baseline schema (T361)
-- ============================================================================
-- Consolidation of the sqlite migration set 001_init..049_chat_messages_
-- user_idx (018 does not exist upstream) into a single PostgreSQL
-- baseline. This is the *final schema state* the sqlite chain produces on
-- a fresh database, translated to PostgreSQL. Data backfills and table
-- rebuilds from the sqlite chain (012/015/017, the *_numbers backfills)
-- are state-neutral on a fresh database and are therefore not replayed;
-- the FTS5 tables and their sync triggers (001/008, search_repo) belong
-- to the postgres full-text task (T365) and are intentionally absent.
--
-- Deliberate deviations from the sqlite shapes (D3: mirror sqlite types):
--   * Types mirrored verbatim: TEXT timestamps (formats 'YYYY-MM-DD
--     HH:MI:SS' and RFC3339), INTEGER booleans, TEXT UUIDs, DOUBLE
--     PRECISION for sqlite REAL (8-byte float; PG's REAL is float4).
--   * `DEFAULT (datetime('now'))` → `to_char(now() AT TIME ZONE 'UTC',
--     'YYYY-MM-DD HH24:MI:SS')` — same UTC, same second precision, same
--     text format sqlite's datetime('now') produces.
--   * updated_at touch triggers (trg_*_touch) become BEFORE UPDATE
--     triggers over the shared orenda_touch_updated_at() function — a
--     row-level UPDATE inside an AFTER trigger would recurse in PG.
--   * mentions.rowid: sqlite rows carry an implicit 1-based rowid and
--     comment_repo orders mentions by it; the PG baseline replaces the
--     implicit rowid with an explicit BIGINT GENERATED ALWAYS AS
--     IDENTITY column + unique index. INSERTs do not name the column,
--     so GENERATED ALWAYS is safe.
--   * tasks.study_course_id → courses(id) is added via ALTER TABLE after
--     courses is created (courses.generator_task_id → tasks makes the
--     dependency circular; sqlite tolerated forward references).
--   * Table creation order deviates from sqlite where a FOREIGN KEY
--     needs its target to exist first (wiki_pages before projects).
--
-- number_seq tables keep the sqlite shape unchanged: the repository
-- high-watermark pattern (`UPDATE ... SET next = next + 1 WHERE id = 1
-- RETURNING next - 1`) is valid PostgreSQL as-is.

-- ---------------------------------------------------------------------------
-- Tables (FK-safe order)
-- ---------------------------------------------------------------------------

CREATE TABLE users (
    id              TEXT PRIMARY KEY,             -- UUIDv7
    email           TEXT NOT NULL UNIQUE,
    password_hash   TEXT NOT NULL,                -- bcrypt
    display_name    TEXT NOT NULL,
    role            TEXT NOT NULL DEFAULT 'owner', -- owner | admin | system (044)
    created_at      TEXT NOT NULL DEFAULT to_char(now() AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS'),
    updated_at      TEXT NOT NULL DEFAULT to_char(now() AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS')
);

CREATE TABLE api_tokens (
    id              TEXT PRIMARY KEY,
    user_id         TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name            TEXT NOT NULL,
    hash            TEXT NOT NULL,                -- bcrypt of opaque token
    scopes          TEXT NOT NULL,                -- JSON array
    last_used_at    TEXT,
    expires_at      TEXT,
    created_at      TEXT NOT NULL DEFAULT to_char(now() AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS')
);

CREATE TABLE agents (
    id              TEXT PRIMARY KEY,
    name            TEXT NOT NULL UNIQUE,
    type            TEXT NOT NULL,                -- JSON array of labels (021)
    description     TEXT,
    token_id        TEXT NOT NULL REFERENCES api_tokens(id) ON DELETE CASCADE,
    last_seen_at    TEXT,
    status          TEXT NOT NULL DEFAULT 'offline',  -- online | offline | disabled
    max_concurrent  INTEGER NOT NULL DEFAULT 3,
    created_at      TEXT NOT NULL DEFAULT to_char(now() AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS')
);

-- Created before projects: projects.wiki_slug REFERENCES wiki_pages(slug).
CREATE TABLE wiki_pages (
    id              TEXT PRIMARY KEY,
    parent_id       TEXT REFERENCES wiki_pages(id) ON DELETE CASCADE,
    slug            TEXT NOT NULL UNIQUE,
    title           TEXT NOT NULL,
    content_md      TEXT,
    position        INTEGER NOT NULL DEFAULT 0,
    created_at      TEXT NOT NULL DEFAULT to_char(now() AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS'),
    updated_at      TEXT NOT NULL DEFAULT to_char(now() AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS'),
    number          INTEGER NOT NULL DEFAULT 0,   -- 037
    content_format  TEXT NOT NULL DEFAULT 'markdown'
);

CREATE TABLE projects (
    id              TEXT PRIMARY KEY,
    name            TEXT NOT NULL,
    color           TEXT NOT NULL DEFAULT '#3b82f6',
    description     TEXT,
    owner_id        TEXT NOT NULL REFERENCES users(id),
    archived        INTEGER NOT NULL DEFAULT 0,   -- sqlite-style boolean
    created_at      TEXT NOT NULL DEFAULT to_char(now() AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS'),
    updated_at      TEXT NOT NULL DEFAULT to_char(now() AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS'),
    wiki_slug       TEXT REFERENCES wiki_pages(slug) ON DELETE SET NULL, -- 034
    number          INTEGER NOT NULL DEFAULT 0,   -- 036
    agents_allowed  INTEGER NOT NULL DEFAULT 0    -- 043 (sqlite-style boolean)
);

CREATE TABLE boards (
    id              TEXT PRIMARY KEY,
    project_id      TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    name            TEXT NOT NULL DEFAULT 'Main',
    position        INTEGER NOT NULL DEFAULT 0,
    created_at      TEXT NOT NULL DEFAULT to_char(now() AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS')
);

CREATE TABLE columns (
    id              TEXT PRIMARY KEY,
    board_id        TEXT NOT NULL REFERENCES boards(id) ON DELETE CASCADE,
    name            TEXT NOT NULL,                -- backlog | todo | in_progress | review | done
    position        DOUBLE PRECISION NOT NULL,    -- float for drag-and-drop ordering
    wip_limit       INTEGER,
    color           TEXT,
    status          TEXT                          -- 020
);

CREATE TABLE tags (
    id              TEXT PRIMARY KEY,
    name            TEXT NOT NULL UNIQUE,
    color           TEXT
);

CREATE TABLE comments (
    id              TEXT PRIMARY KEY,
    target_type     TEXT NOT NULL,                 -- task | page | event
    target_id       TEXT NOT NULL,
    author_type     TEXT NOT NULL,                 -- user | agent
    author_id       TEXT NOT NULL,
    body_md         TEXT NOT NULL,
    created_at      TEXT NOT NULL DEFAULT to_char(now() AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS'),
    edited_at       TEXT                           -- 041
);

-- rowid replaces the sqlite implicit rowid: comment_repo lists mentions in
-- body order via `ORDER BY rowid ASC`. INSERTs never name the column, so
-- GENERATED ALWAYS cannot conflict with application code.
CREATE TABLE mentions (
    rowid           BIGINT GENERATED ALWAYS AS IDENTITY, -- mirrors sqlite rowid ordering
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
    created_at      TEXT NOT NULL DEFAULT to_char(now() AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS')
);

CREATE TABLE wiki_links (
    from_page_id    TEXT NOT NULL REFERENCES wiki_pages(id) ON DELETE CASCADE,
    to_page_id      TEXT NOT NULL REFERENCES wiki_pages(id) ON DELETE CASCADE,
    PRIMARY KEY (from_page_id, to_page_id)
);

CREATE TABLE notifications (
    id              TEXT PRIMARY KEY,
    user_id         TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    type            TEXT NOT NULL,
    target_type     TEXT,
    target_id       TEXT,
    payload         TEXT,
    read_at         TEXT,
    dedup_key       TEXT NOT NULL UNIQUE,
    created_at      TEXT NOT NULL DEFAULT to_char(now() AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS')
);

CREATE TABLE bot_subscriptions (
    id              TEXT PRIMARY KEY,
    user_id         TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    bot_type        TEXT NOT NULL,                 -- console | vk | telegram | email | webhook
    target_address  TEXT NOT NULL,
    events          TEXT NOT NULL,                 -- JSON array of event types
    enabled         INTEGER NOT NULL DEFAULT 1,    -- sqlite-style boolean
    created_at      TEXT NOT NULL DEFAULT to_char(now() AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS')
);

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
    created_at      TEXT NOT NULL DEFAULT to_char(now() AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS')
);

CREATE TABLE sync_ops (
    client_id   TEXT PRIMARY KEY,           -- PWA-side idempotency key
    server_id   TEXT NOT NULL,              -- the row created for this op
    op          TEXT NOT NULL,              -- create_task | update_task | ...
    target      TEXT NOT NULL,
    applied_at  TEXT NOT NULL DEFAULT to_char(now() AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS')
);

-- Final shape after the 012/015/016 rebuilds + 022..042 column additions.
CREATE TABLE tasks (
    id              TEXT PRIMARY KEY,
    project_id      TEXT REFERENCES projects(id) ON DELETE CASCADE,
    parent_task_id  TEXT REFERENCES tasks(id) ON DELETE CASCADE,
    column_id       TEXT REFERENCES columns(id) ON DELETE SET NULL,
    title           TEXT NOT NULL,
    description     TEXT,
    status          TEXT NOT NULL DEFAULT 'todo',
    priority        TEXT NOT NULL DEFAULT 'medium',
    assignee_type   TEXT,
    assignee_id     TEXT,
    awaiting        TEXT NOT NULL DEFAULT 'none',
    context_md      TEXT,
    agent_notes     TEXT,
    due_at          TEXT,
    started_at      TEXT,
    claimed_at      TEXT,
    completed_at    TEXT,
    time_estimate_s INTEGER,
    time_spent_s    INTEGER NOT NULL DEFAULT 0,
    position        DOUBLE PRECISION NOT NULL DEFAULT 0,
    created_at      TEXT NOT NULL DEFAULT to_char(now() AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS'),
    updated_at      TEXT NOT NULL DEFAULT to_char(now() AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS'),
    start_at        TEXT,
    end_at          TEXT,
    all_day         INTEGER NOT NULL DEFAULT 0,   -- sqlite-style boolean
    color           TEXT,
    -- RRULE carried over from the legacy events table (012/015).
    recurrence      TEXT,
    study_course_id TEXT,                          -- 022 (FK added below — circular with courses)
    created_by_type TEXT NOT NULL DEFAULT 'user', -- 024
    created_by_id   TEXT,                         -- 024
    number          INTEGER NOT NULL DEFAULT 0,   -- 033
    blocked_prev_status TEXT                       -- 042
);

CREATE TABLE courses (
    id                TEXT PRIMARY KEY,
    title             TEXT NOT NULL,
    intent_md         TEXT NOT NULL DEFAULT '',
    level             TEXT NOT NULL DEFAULT 'beginner',  -- beginner|intermediate|advanced
    pace              TEXT NOT NULL DEFAULT 'casual',    -- casual|regular|intensive
    status            TEXT NOT NULL DEFAULT 'draft',     -- draft|review|active|done|archived
    owner_id          TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    generator_task_id TEXT REFERENCES tasks(id) ON DELETE SET NULL,
    created_at        TEXT NOT NULL DEFAULT to_char(now() AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS'),
    updated_at        TEXT NOT NULL DEFAULT to_char(now() AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS'),
    pace_notes_md     TEXT NOT NULL DEFAULT '',
    number            INTEGER NOT NULL DEFAULT 0    -- 038
);

-- Deferred half of the tasks ⇄ courses circular dependency (see above).
ALTER TABLE tasks
    ADD CONSTRAINT tasks_study_course_fk
    FOREIGN KEY (study_course_id) REFERENCES courses(id) ON DELETE SET NULL;

CREATE TABLE task_locks (
    task_id         TEXT PRIMARY KEY REFERENCES tasks(id) ON DELETE CASCADE,
    agent_id        TEXT NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
    acquired_at     TEXT NOT NULL DEFAULT to_char(now() AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS')
);

CREATE TABLE checklists (
    id              TEXT PRIMARY KEY,
    task_id         TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    title           TEXT NOT NULL,
    position        INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE checklist_items (
    id              TEXT PRIMARY KEY,
    checklist_id    TEXT NOT NULL REFERENCES checklists(id) ON DELETE CASCADE,
    title           TEXT NOT NULL,
    done            INTEGER NOT NULL DEFAULT 0,    -- sqlite-style boolean
    position        INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE task_tags (
    task_id         TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    tag_id          TEXT NOT NULL REFERENCES tags(id) ON DELETE CASCADE,
    PRIMARY KEY (task_id, tag_id)
);

CREATE TABLE task_activity (
    id              TEXT PRIMARY KEY,
    task_id         TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    actor_type      TEXT NOT NULL,
    actor_id        TEXT NOT NULL,
    action          TEXT NOT NULL,
    payload         TEXT,
    created_at      TEXT NOT NULL DEFAULT to_char(now() AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS')
);

CREATE TABLE time_entries (
    id              TEXT PRIMARY KEY,
    task_id         TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    agent_id        TEXT,                          -- 007: NULL for user-run timers
    started_at      TEXT NOT NULL,
    ended_at        TEXT,
    duration_s      INTEGER,
    source          TEXT NOT NULL DEFAULT 'manual'
);

CREATE TABLE task_dependencies (
    task_id              TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    depends_on_task_id   TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    PRIMARY KEY (task_id, depends_on_task_id)
);

CREATE TABLE course_modules (
    id              TEXT PRIMARY KEY,
    course_id       TEXT NOT NULL REFERENCES courses(id) ON DELETE CASCADE,
    title           TEXT NOT NULL,
    description     TEXT NOT NULL DEFAULT '',
    position        INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE course_lessons (
    id              TEXT PRIMARY KEY,
    module_id       TEXT NOT NULL REFERENCES course_modules(id) ON DELETE CASCADE,
    title           TEXT NOT NULL,
    content_md      TEXT NOT NULL DEFAULT '',
    status          TEXT NOT NULL DEFAULT 'locked',    -- locked|open|done
    position        INTEGER NOT NULL DEFAULT 0,
    task_id         TEXT REFERENCES tasks(id) ON DELETE SET NULL,
    completed_at    TEXT,                              -- 035
    number          INTEGER NOT NULL DEFAULT 0         -- 039
);

CREATE TABLE course_quizzes (
    id              TEXT PRIMARY KEY,
    lesson_id       TEXT NOT NULL REFERENCES course_lessons(id) ON DELETE CASCADE,
    position        INTEGER NOT NULL DEFAULT 0,
    question_md     TEXT NOT NULL,
    expected_md     TEXT NOT NULL DEFAULT '',
    kind            TEXT NOT NULL DEFAULT 'open'      -- open|exact
);

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
    created_at          TEXT NOT NULL DEFAULT to_char(now() AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS'),
    resolved_at         TEXT
);

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
    created_at      TEXT NOT NULL DEFAULT to_char(now() AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS')
);

CREATE TABLE project_activity (
    id              TEXT PRIMARY KEY,
    project_id      TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    actor_type      TEXT NOT NULL,  -- user | agent
    actor_id        TEXT NOT NULL,
    kind            TEXT NOT NULL,  -- description_changed
    payload         TEXT,           -- free-form small JSON, optional
    created_at      TEXT NOT NULL DEFAULT to_char(now() AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS')
);

CREATE TABLE task_retracted (
    id              TEXT PRIMARY KEY,
    task_id         TEXT NOT NULL,
    snapshot_json   TEXT NOT NULL,
    actor_type      TEXT NOT NULL,
    actor_id        TEXT NOT NULL,
    retracted_at    TEXT NOT NULL DEFAULT to_char(now() AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS')
);

CREATE TABLE chat_messages (
    id            TEXT PRIMARY KEY,
    thread_id     TEXT NOT NULL,
    sender_type   TEXT NOT NULL,            -- 'user' | 'agent'
    body_md       TEXT NOT NULL,
    command       TEXT,                     -- non-null when sender_type='user' AND message starts with '/'
    result_ref    TEXT,                     -- for '/plan' → study_proposal id, etc.
    created_at    TEXT NOT NULL DEFAULT to_char(now() AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS'),
    user_id       TEXT NOT NULL DEFAULT ''  -- 048
);

CREATE TABLE chat_threads (
    id         TEXT PRIMARY KEY,
    user_id    TEXT NOT NULL,
    thread_id  TEXT NOT NULL,
    created_at TEXT NOT NULL DEFAULT to_char(now() AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS'),
    UNIQUE(user_id, thread_id)
);

CREATE TABLE wiki_blocks (
    id              TEXT PRIMARY KEY,
    page_id         TEXT NOT NULL REFERENCES wiki_pages(id) ON DELETE CASCADE,
    parent_block_id TEXT REFERENCES wiki_blocks(id) ON DELETE CASCADE,
    position        INTEGER NOT NULL DEFAULT 0,
    type            TEXT NOT NULL,
    data            TEXT NOT NULL DEFAULT '{}',
    created_at      TEXT NOT NULL DEFAULT to_char(now() AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS'),
    updated_at      TEXT NOT NULL DEFAULT to_char(now() AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS')
);

CREATE TABLE project_agents (
    project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    agent_id   TEXT NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
    added_by   TEXT NOT NULL REFERENCES users(id),
    added_at   TEXT NOT NULL DEFAULT to_char(now() AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS'),
    PRIMARY KEY (project_id, agent_id)
);

CREATE TABLE tutor_messages (
    id          TEXT PRIMARY KEY,
    lesson_id   TEXT NOT NULL REFERENCES course_lessons(id) ON DELETE CASCADE,
    user_id     TEXT NOT NULL,
    role        TEXT NOT NULL CHECK (role IN ('user','agent')),
    body_md     TEXT NOT NULL,
    created_at  TEXT NOT NULL DEFAULT to_char(now() AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS')
);

CREATE TABLE lesson_reviews (
    id           TEXT PRIMARY KEY,
    lesson_id    TEXT NOT NULL REFERENCES course_lessons(id) ON DELETE CASCADE,
    user_id      TEXT NOT NULL,
    step         INTEGER NOT NULL DEFAULT 0,
    due_at       TEXT NOT NULL,                        -- RFC3339 UTC
    last_result  TEXT CHECK (last_result IN ('pass','fail') OR last_result IS NULL),
    completed_at TEXT,                                 -- set when the ladder is fully walked
    created_at   TEXT NOT NULL DEFAULT to_char(now() AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS')
);

-- Number high-watermarks: one singleton row each, driven by
-- `UPDATE ... SET next = next + 1 WHERE id = 1 RETURNING next - 1`.
CREATE TABLE task_number_seq (
    id   INTEGER PRIMARY KEY CHECK (id = 1),
    next INTEGER NOT NULL
);

-- sqlite 033 seeds `SELECT 1, COALESCE(MAX(number), 0) + 1 FROM tasks`,
-- which is (1, 1) on a fresh database; without the seed row the first
-- `UPDATE ... RETURNING next - 1` in the repositories finds no rows.
INSERT INTO task_number_seq (id, next) VALUES (1, 1);

CREATE TABLE project_number_seq (
    id   INTEGER PRIMARY KEY CHECK (id = 1),
    next INTEGER NOT NULL
);

-- sqlite 036 seed, see task_number_seq above.
INSERT INTO project_number_seq (id, next) VALUES (1, 1);

CREATE TABLE wiki_page_number_seq (
    id   INTEGER PRIMARY KEY CHECK (id = 1),
    next INTEGER NOT NULL
);

-- sqlite 037 seed, see task_number_seq above.
INSERT INTO wiki_page_number_seq (id, next) VALUES (1, 1);

CREATE TABLE course_number_seq (
    id   INTEGER PRIMARY KEY CHECK (id = 1),
    next INTEGER NOT NULL
);

-- sqlite 038 seed, see task_number_seq above.
INSERT INTO course_number_seq (id, next) VALUES (1, 1);

CREATE TABLE lesson_number_seq (
    id   INTEGER PRIMARY KEY CHECK (id = 1),
    next INTEGER NOT NULL
);

-- sqlite 039 seed, see task_number_seq above.
INSERT INTO lesson_number_seq (id, next) VALUES (1, 1);

-- ---------------------------------------------------------------------------
-- Indexes
-- ---------------------------------------------------------------------------

CREATE INDEX idx_api_tokens_user ON api_tokens(user_id);
CREATE INDEX idx_api_tokens_hash ON api_tokens(hash);
CREATE INDEX idx_users_email ON users(email);
CREATE INDEX idx_agents_status ON agents(status);
CREATE INDEX idx_agents_last_seen ON agents(last_seen_at);
CREATE INDEX idx_projects_owner ON projects(owner_id);
CREATE INDEX idx_projects_wiki_slug ON projects(wiki_slug);
CREATE UNIQUE INDEX idx_projects_number ON projects(number);
CREATE INDEX idx_boards_project ON boards(project_id);
CREATE INDEX idx_columns_board ON columns(board_id);
CREATE UNIQUE INDEX idx_columns_board_status ON columns(board_id, status);
CREATE INDEX idx_comments_target ON comments(target_type, target_id);
CREATE INDEX idx_comments_author ON comments(author_type, author_id, created_at);
CREATE UNIQUE INDEX idx_mentions_rowid ON mentions(rowid);
CREATE INDEX idx_attachments_target ON attachments(target_type, target_id);
CREATE INDEX idx_attachments_sha256 ON attachments(sha256);
CREATE INDEX idx_pages_parent ON wiki_pages(parent_id);
CREATE UNIQUE INDEX idx_wiki_pages_number ON wiki_pages(number);
CREATE INDEX idx_wiki_links_to ON wiki_links(to_page_id);
CREATE INDEX idx_wiki_links_from ON wiki_links(from_page_id);
CREATE INDEX idx_notif_user ON notifications(user_id, read_at);
CREATE INDEX idx_notifications_user_unread ON notifications(user_id, read_at);
CREATE INDEX idx_notifications_target ON notifications(target_type, target_id);
CREATE INDEX idx_subs_user ON bot_subscriptions(user_id);
CREATE INDEX idx_bot_subs_user ON bot_subscriptions(user_id, enabled);
CREATE INDEX idx_backup_log_created ON backup_log(created_at);
CREATE INDEX idx_backup_log_type_created ON backup_log(type, created_at);
CREATE INDEX idx_sync_ops_target ON sync_ops(target);
CREATE INDEX idx_tasks_project ON tasks(project_id);
CREATE INDEX idx_tasks_status ON tasks(status);
CREATE INDEX idx_tasks_assignee ON tasks(assignee_type, assignee_id);
CREATE INDEX idx_tasks_due ON tasks(due_at);
CREATE INDEX idx_tasks_parent ON tasks(parent_task_id);
CREATE INDEX idx_tasks_project_column_position ON tasks(project_id, column_id, position);
CREATE INDEX idx_tasks_assignee_status ON tasks(assignee_type, assignee_id, status);
CREATE INDEX idx_tasks_time ON tasks(start_at, end_at)
    WHERE start_at IS NOT NULL AND end_at IS NOT NULL;
CREATE UNIQUE INDEX idx_tasks_number ON tasks(number);
CREATE INDEX idx_tasks_created_by ON tasks(created_by_type, created_by_id)
    WHERE created_by_id IS NOT NULL;
CREATE INDEX idx_tasks_study_course ON tasks(study_course_id)
    WHERE study_course_id IS NOT NULL;
CREATE INDEX idx_activity_task ON task_activity(task_id, created_at);
CREATE INDEX idx_activity_actor ON task_activity(actor_type, actor_id, created_at);
CREATE INDEX idx_time_task ON time_entries(task_id);
CREATE INDEX idx_time_entries_agent ON time_entries(agent_id, started_at);
CREATE INDEX idx_time_entries_open ON time_entries(agent_id) WHERE ended_at IS NULL;
CREATE INDEX idx_checklists_task ON checklists(task_id);
CREATE INDEX idx_task_locks_agent ON task_locks(agent_id);
CREATE INDEX idx_task_deps_task ON task_dependencies(task_id);
CREATE INDEX idx_task_deps_depends_on ON task_dependencies(depends_on_task_id);
CREATE INDEX idx_courses_owner ON courses(owner_id);
CREATE INDEX idx_courses_status ON courses(status);
CREATE UNIQUE INDEX idx_courses_number ON courses(number);
CREATE INDEX idx_course_modules_course ON course_modules(course_id, position);
CREATE INDEX idx_course_lessons_module ON course_lessons(module_id, position);
CREATE INDEX idx_course_lessons_completed ON course_lessons(module_id, completed_at) WHERE completed_at IS NOT NULL;
CREATE UNIQUE INDEX idx_lessons_number ON course_lessons(number);
CREATE INDEX idx_course_quizzes_lesson ON course_quizzes(lesson_id, position);
CREATE INDEX idx_study_proposals_status_created ON study_proposals(status, created_at);
CREATE INDEX idx_course_activity_course ON course_activity(course_id, created_at);
CREATE INDEX idx_course_activity_actor ON course_activity(actor_id, created_at);
CREATE INDEX idx_project_activity_project ON project_activity(project_id, created_at);
CREATE INDEX idx_project_activity_actor ON project_activity(actor_id, created_at);
CREATE INDEX idx_chat_messages_thread ON chat_messages(thread_id, created_at DESC);
CREATE INDEX idx_chat_messages_user ON chat_messages(user_id, created_at DESC);
CREATE INDEX idx_wiki_blocks_page ON wiki_blocks(page_id, parent_block_id, position);
CREATE INDEX idx_project_agents_agent ON project_agents(agent_id);
CREATE INDEX idx_tutor_messages_lesson_user ON tutor_messages(lesson_id, user_id, created_at);
CREATE INDEX idx_tutor_messages_user ON tutor_messages(user_id);
CREATE INDEX idx_lesson_reviews_due_at ON lesson_reviews(due_at);
CREATE INDEX idx_lesson_reviews_lesson ON lesson_reviews(lesson_id);
CREATE INDEX idx_lesson_reviews_user_due ON lesson_reviews(user_id, due_at);

-- ---------------------------------------------------------------------------
-- updated_at touch triggers (sqlite: trg_users_touch, trg_projects_touch,
-- trg_wiki_pages_touch, trg_tasks_touch — one shared PG function instead of
-- per-table UPDATE statements, which would recurse in PG).
-- ---------------------------------------------------------------------------

CREATE FUNCTION orenda_touch_updated_at() RETURNS trigger AS $$
BEGIN
    NEW.updated_at := to_char(now() AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS');
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trg_users_touch
BEFORE UPDATE ON users
FOR EACH ROW EXECUTE FUNCTION orenda_touch_updated_at();

CREATE TRIGGER trg_projects_touch
BEFORE UPDATE ON projects
FOR EACH ROW EXECUTE FUNCTION orenda_touch_updated_at();

CREATE TRIGGER trg_wiki_pages_touch
BEFORE UPDATE ON wiki_pages
FOR EACH ROW EXECUTE FUNCTION orenda_touch_updated_at();

CREATE TRIGGER trg_tasks_touch
BEFORE UPDATE ON tasks
FOR EACH ROW EXECUTE FUNCTION orenda_touch_updated_at();
