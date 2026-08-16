-- ============================================================================
-- 005_comments_attachments.sql — comments/mentions/attachments/activity
-- ============================================================================
-- Phase 3, task 3.2.
--
-- All four tables (comments, mentions, attachments, task_activity) exist in
-- 001_init.sql. This migration is additive:
--
--   * idx_comments_author — agent inbox / "what did I write?" queries
--   * idx_attachments_sha256 — dedup check before storing a duplicate file
--   * idx_activity_actor — agent activity feed (recent first by actor)
--   * idx_notifications_dedup — covered by UNIQUE(dedup_key) already;
--     the index on (user_id, read_at) lives in 001_init.
--   * idx_bot_subs_bot_event — bot dispatch (Phase 6) will benefit

CREATE INDEX IF NOT EXISTS idx_comments_author
    ON comments(author_type, author_id, created_at);

CREATE INDEX IF NOT EXISTS idx_attachments_sha256
    ON attachments(sha256);

CREATE INDEX IF NOT EXISTS idx_activity_actor
    ON task_activity(actor_type, actor_id, created_at);

-- updated_at trigger on wiki_pages keeps in sync (the table exists in 001
-- but lacks the trigger). Pages are user-edited; mirrors (Phase 7) rely on
-- updated_at being accurate.
CREATE TRIGGER IF NOT EXISTS trg_wiki_pages_touch
AFTER UPDATE ON wiki_pages
FOR EACH ROW
BEGIN
    UPDATE wiki_pages SET updated_at = datetime('now') WHERE id = OLD.id;
END;

-- updated_at trigger on events (calendar) — same rationale.
CREATE TRIGGER IF NOT EXISTS trg_events_touch
AFTER UPDATE ON events
FOR EACH ROW
BEGIN
    UPDATE events SET updated_at = datetime('now') WHERE id = OLD.id;
END;