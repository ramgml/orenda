-- ============================================================================
-- 009_notifications.down.sql — drop notification + bot subscription indexes
-- ============================================================================

DROP INDEX IF EXISTS idx_notifications_user_unread;
DROP INDEX IF EXISTS idx_notifications_target;
DROP INDEX IF EXISTS idx_bot_subs_user;
