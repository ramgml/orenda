-- 002_search.down.sql — drop the full-text search columns and indexes.
-- GIN indexes are standalone objects in postgres, so they are dropped
-- explicitly; every statement is idempotent.

DROP INDEX IF EXISTS idx_wiki_pages_search;
DROP INDEX IF EXISTS idx_tasks_search;
DROP INDEX IF EXISTS idx_comments_search;

ALTER TABLE wiki_pages DROP COLUMN IF EXISTS search_vec;
ALTER TABLE tasks DROP COLUMN IF EXISTS search_vec;
ALTER TABLE comments DROP COLUMN IF EXISTS search_vec;
