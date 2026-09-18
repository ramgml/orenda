-- ============================================================================
-- 048_chat_messages_user_id.down.sql — drop chat authorship (task 9)
-- ============================================================================
-- SQLite has no DROP COLUMN before 3.35 and modernc builds vary; the
-- round-trip down path rebuilds the table without the column, dropping
-- authorship (history rows survive keyed by thread only).

CREATE TABLE chat_messages_migrate048_down (
    id          TEXT PRIMARY KEY,
    thread_id   TEXT NOT NULL,
    sender_type TEXT NOT NULL,
    body_md     TEXT NOT NULL,
    command     TEXT,
    result_ref  TEXT,
    created_at  TEXT NOT NULL DEFAULT (datetime('now'))
);

INSERT INTO chat_messages_migrate048_down (id, thread_id, sender_type, body_md, command, result_ref, created_at)
    SELECT id, thread_id, sender_type, body_md, command, result_ref, created_at FROM chat_messages;

DROP TABLE chat_messages;
ALTER TABLE chat_messages_migrate048_down RENAME TO chat_messages;

CREATE INDEX idx_chat_messages_thread ON chat_messages(thread_id, created_at DESC);
