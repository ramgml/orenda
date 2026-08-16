-- ============================================================================
-- 008_wiki.down.sql — drop FTS5 tables, sync triggers, and link indexes
-- ============================================================================
-- Reverse the additive wiki search layer. The wiki_pages /
-- wiki_links tables themselves live in 001_init.sql; their DROP
-- belongs there.

DROP TRIGGER IF EXISTS trg_wiki_pages_fts_insert;
DROP TRIGGER IF EXISTS trg_wiki_pages_fts_update;
DROP TRIGGER IF EXISTS trg_wiki_pages_fts_delete;
DROP TRIGGER IF EXISTS trg_tasks_fts_insert;
DROP TRIGGER IF EXISTS trg_tasks_fts_update;
DROP TRIGGER IF EXISTS trg_tasks_fts_delete;
DROP TRIGGER IF EXISTS trg_comments_fts_insert;
DROP TRIGGER IF EXISTS trg_comments_fts_update;
DROP TRIGGER IF EXISTS trg_comments_fts_delete;

DROP INDEX IF EXISTS idx_wiki_links_to;
DROP INDEX IF EXISTS idx_wiki_links_from;

DROP TABLE IF EXISTS comments_fts;
DROP TABLE IF EXISTS tasks_fts;
DROP TABLE IF EXISTS pages_fts;
