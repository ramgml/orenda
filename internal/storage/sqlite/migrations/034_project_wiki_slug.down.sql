-- Rollback for migration 034.

DROP INDEX IF EXISTS idx_projects_wiki_slug;
-- SQLite ALTER TABLE ... DROP COLUMN is supported in 3.35+; orenda
-- requires 3.38+ (modernc.org/sqlite bundles 3.45+), so the drop is
-- safe. The FK constraint is dropped with the column.
ALTER TABLE projects DROP COLUMN wiki_slug;