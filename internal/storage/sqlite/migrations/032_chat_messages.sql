-- Phase 32.11 chat-agent: persistent chat history between the
-- user and the planning agent. Each row is one user message or
-- one agent response; the wire shape pairs them by thread_id
-- (the user typically opens one chat per session, but the
-- schema supports multiple if the UI later adds that).
--
-- Persisted rows let the Dashboard re-render the last N messages
-- on page load without a separate "history" endpoint. The WS
-- topic only carries new messages; replay is a SELECT.
CREATE TABLE chat_messages (
    id            TEXT PRIMARY KEY,
    thread_id     TEXT NOT NULL,
    sender_type   TEXT NOT NULL,            -- 'user' | 'agent'
    body_md       TEXT NOT NULL,
    command       TEXT,                     -- non-null when sender_type='user' AND message starts with '/'
    result_ref    TEXT,                     -- for '/plan' → study_proposal id, etc.
    created_at    TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX idx_chat_messages_thread ON chat_messages(thread_id, created_at DESC);
