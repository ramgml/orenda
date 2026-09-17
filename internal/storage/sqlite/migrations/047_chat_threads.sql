-- ============================================================================
-- 047_chat_threads.sql — per-user dashboard chat threads (task 9)
-- ============================================================================
-- The Dashboard chat pane used to key threads by thread_id alone, so any
-- user replaying a thread id saw the whole history. chat_threads maps the
-- (user_id, thread_id) pair for the signed-in user: POST /dashboard/chat
-- upserts a row, GET /dashboard/chat/{thread} accepts only threads this
-- user opened.
--
-- thread_id stays a free string (the UI uses "default" today); the UNIQUE
-- constraint makes the upsert idempotent across retries.

CREATE TABLE chat_threads (
    id         TEXT PRIMARY KEY,
    user_id    TEXT NOT NULL,
    thread_id  TEXT NOT NULL,
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    UNIQUE(user_id, thread_id)
);
