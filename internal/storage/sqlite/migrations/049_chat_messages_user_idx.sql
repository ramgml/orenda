-- ============================================================================
-- 049_chat_messages_user_idx.sql — index the per-user chat replay (task 9)
-- ============================================================================
-- GET /dashboard/chat/{thread} filters by user_id after 048; this index
-- keeps that scan cheap for the newest-50 window.

CREATE INDEX idx_chat_messages_user ON chat_messages(user_id, created_at DESC);
