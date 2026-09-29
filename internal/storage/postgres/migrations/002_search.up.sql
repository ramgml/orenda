-- ============================================================================
-- 002_search.up.sql — full-text search: generated tsvector columns + GIN
-- ============================================================================
-- T365 (wiki:storage-adapters D6). The postgres counterpart of the sqlite
-- FTS5 objects created in 008_wiki.sql (recreated after the tasks rebuild
-- in 015_inbox_no_project.sql).
--
-- Parity with the sqlite FTS5 setup (008_wiki.sql):
--
--   sqlite FTS5 table | indexed columns            | PG generated column
--   ------------------+----------------------------+---------------------
--   pages_fts         | title, content_md          | wiki_pages.search_vec
--   tasks_fts         | title, description,        | tasks.search_vec
--                     | context_md                 |
--   comments_fts      | body_md                    | comments.search_vec
--
-- The same search fields feed every index on both dialects, so a term
-- matches the same rows regardless of driver.
--
-- Design notes:
--
--   * The text search configuration is the built-in 'simple': it
--     lowercases tokens and never stems — the same behaviour the FTS5
--     'unicode61' tokenizer provides on the sqlite side. Cyrillic and
--     other non-ASCII terms fold case through lower(), which requires a
--     non-C lc_ctype on the cluster (any *.UTF-8 locale); with lc_ctype=C
--     non-ASCII case folding silently degrades.
--   * 'simple' does not fold diacritics. The sqlite tokenizer runs with
--     remove_diacritics 2, so "café" also matches "cafe" there; on
--     postgres both sides must carry the same accent. Known, accepted
--     divergence (no unaccent extension dependency in this task).
--   * Columns are GENERATED ALWAYS ... STORED: postgres maintains them
--     on INSERT/UPDATE by itself. The sqlite setup needs nine sync
--     triggers (008/015) because FTS5 virtual tables are not reactive;
--     postgres needs none.
--   * GIN indexes keep term lookups on par with the FTS5 index; the
--     plain (non-trgm) variant answers tsquery matches, which is all
--     search_repo issues.
--   * One tsvector per table concatenates the search fields, mirroring
--     the FTS5 shape where every indexed column feeds one MATCH index.
--     Position numbering continues across the concatenated parts, so a
--     phrase query can (unlike FTS5) span the title/content boundary —
--     a harmless, documented edge.
--
-- Ranking moves from FTS5 bm25 to ts_rank in the repository (see
-- search_repo.go): bm25 weights every field equally by default and so
-- does the plain concatenation here.

-- wiki_pages: title + content_md (pages_fts).
ALTER TABLE wiki_pages ADD COLUMN search_vec tsvector GENERATED ALWAYS AS (
    to_tsvector('simple', coalesce(title, '')) ||
    to_tsvector('simple', coalesce(content_md, ''))
) STORED;
CREATE INDEX idx_wiki_pages_search ON wiki_pages USING GIN (search_vec);

-- tasks: title + description + context_md (tasks_fts).
ALTER TABLE tasks ADD COLUMN search_vec tsvector GENERATED ALWAYS AS (
    to_tsvector('simple', coalesce(title, '')) ||
    to_tsvector('simple', coalesce(description, '')) ||
    to_tsvector('simple', coalesce(context_md, ''))
) STORED;
CREATE INDEX idx_tasks_search ON tasks USING GIN (search_vec);

-- comments: body_md (comments_fts).
ALTER TABLE comments ADD COLUMN search_vec tsvector GENERATED ALWAYS AS (
    to_tsvector('simple', coalesce(body_md, ''))
) STORED;
CREATE INDEX idx_comments_search ON comments USING GIN (search_vec);
