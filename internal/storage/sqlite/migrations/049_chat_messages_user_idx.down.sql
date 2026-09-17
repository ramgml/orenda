-- ============================================================================
-- 049_chat_messages_user_idx.down.sql — drop the per-user chat index (task 9)
-- ============================================================================
-- Lossless: the index is derived state.

DROP INDEX IF EXISTS idx_chat_messages_user;
