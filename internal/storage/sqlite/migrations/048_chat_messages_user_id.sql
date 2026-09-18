-- ============================================================================
-- 048_chat_messages_user_id.sql — scope chat messages to their author (task 9)
-- ============================================================================
-- chat_messages rows were anonymous: the thread replay filtered by
-- thread_id only. user_id records who wrote a user message (and which
-- user an agent reply belongs to) so GET /dashboard/chat/{thread} can
-- return only that user's history.
--
-- DEFAULT '' keeps pre-048 rows loadable; the replay treats '' as legacy
-- data owned by nobody.

ALTER TABLE chat_messages ADD COLUMN user_id TEXT NOT NULL DEFAULT '';
