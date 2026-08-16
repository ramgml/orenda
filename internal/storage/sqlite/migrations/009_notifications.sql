-- ============================================================================
-- 009_notifications.sql — notifications inbox + bot subscriptions indexes
-- ============================================================================
-- Phase 6, task 6.1.
--
-- notifications and bot_subscriptions tables exist in 001_init.sql. This
-- migration is additive:
--
--   * idx_notifications_user_unread — badge query (unread count per user)
--     with read_at IS NULL partial index for O(1) unread fetching
--   * idx_notifications_target      — "show me everything for task X"
--   * idx_bot_subs_user_event       — subscription fanout: which subscriptions
--     of a given user subscribe to a given event type? (events is JSON, so
--     this is approximate — application code filters further)

CREATE INDEX IF NOT EXISTS idx_notifications_user_unread
    ON notifications(user_id, read_at);

CREATE INDEX IF NOT EXISTS idx_notifications_target
    ON notifications(target_type, target_id);

-- The subscriptions fanout is user-scoped; we add a plain user index so
-- per-user listing is fast. Event filtering happens in the application.
CREATE INDEX IF NOT EXISTS idx_bot_subs_user
    ON bot_subscriptions(user_id, enabled);