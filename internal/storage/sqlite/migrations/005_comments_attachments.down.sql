-- ============================================================================
-- 005_comments_attachments.down.sql — drop indexes + touch triggers
-- ============================================================================

DROP INDEX IF EXISTS idx_comments_author;
DROP INDEX IF EXISTS idx_attachments_sha256;
DROP INDEX IF EXISTS idx_activity_actor;
DROP TRIGGER IF EXISTS trg_wiki_pages_touch;
DROP TRIGGER IF EXISTS trg_events_touch;
