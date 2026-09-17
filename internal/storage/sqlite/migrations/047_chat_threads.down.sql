-- ============================================================================
-- 047_chat_threads.down.sql — drop per-user chat thread ownership (task 9)
-- ============================================================================
-- Lossy by design (down is for round-trip tests): thread ownership rows are
-- gone, chat_messages themselves are untouched.

DROP TABLE IF EXISTS chat_threads;
