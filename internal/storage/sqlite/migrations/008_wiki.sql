-- ============================================================================
-- 008_wiki.sql — wiki pages, links, and FTS5 search indexes
-- ============================================================================
-- Phase 5, task 5.1.
--
-- wiki_pages and wiki_links tables already exist in 001_init.sql. This
-- migration is additive:
--
--   * FTS5 virtual tables (pages_fts, tasks_fts, comments_fts) with the
--     unicode61 tokenizer + remove_diacritics 2 (Cyrillic support)
--   * Sync triggers so INSERT/UPDATE/DELETE on the source tables update
--     the FTS index
--   * Indexes on wiki_pages.slug (UNIQUE already provides one) and
--     wiki_links.to_page_id (backlink queries)
--
-- The FTS tables are content='...' (external content); rowids map to the
-- source tables' rowids via content_rowid='rowid'. We don't need
-- stored-content — only BM25 ranking.

-- ----------------------------------------------------------------------------
-- FTS5 tables
-- ----------------------------------------------------------------------------

CREATE VIRTUAL TABLE IF NOT EXISTS pages_fts USING fts5(
    title,
    content_md,
    content      = 'wiki_pages',
    content_rowid = 'rowid',
    tokenize      = 'unicode61 remove_diacritics 2'
);

CREATE VIRTUAL TABLE IF NOT EXISTS tasks_fts USING fts5(
    title,
    description,
    context_md,
    content      = 'tasks',
    content_rowid = 'rowid',
    tokenize      = 'unicode61 remove_diacritics 2'
);

CREATE VIRTUAL TABLE IF NOT EXISTS comments_fts USING fts5(
    body_md,
    content      = 'comments',
    content_rowid = 'rowid',
    tokenize      = 'unicode61 remove_diacritics 2'
);

-- ----------------------------------------------------------------------------
-- Sync triggers: keep FTS in sync with the source tables.
-- ----------------------------------------------------------------------------
--
-- Each trigger inserts/deletes the FTS row for the same rowid.
-- The `INSERT ... (rowid, ...) VALUES (old.rowid, ...)` form is the FTS5
-- "contentless" delete pattern.

-- wiki_pages
CREATE TRIGGER IF NOT EXISTS trg_wiki_pages_fts_insert AFTER INSERT ON wiki_pages BEGIN
    INSERT INTO pages_fts(rowid, title, content_md)
    VALUES (new.rowid, new.title, new.content_md);
END;

CREATE TRIGGER IF NOT EXISTS trg_wiki_pages_fts_update AFTER UPDATE ON wiki_pages BEGIN
    INSERT INTO pages_fts(pages_fts, rowid, title, content_md)
    VALUES ('delete', old.rowid, old.title, old.content_md);
    INSERT INTO pages_fts(rowid, title, content_md)
    VALUES (new.rowid, new.title, new.content_md);
END;

CREATE TRIGGER IF NOT EXISTS trg_wiki_pages_fts_delete AFTER DELETE ON wiki_pages BEGIN
    INSERT INTO pages_fts(pages_fts, rowid, title, content_md)
    VALUES ('delete', old.rowid, old.title, old.content_md);
END;

-- tasks
CREATE TRIGGER IF NOT EXISTS trg_tasks_fts_insert AFTER INSERT ON tasks BEGIN
    INSERT INTO tasks_fts(rowid, title, description, context_md)
    VALUES (new.rowid, new.title, new.description, new.context_md);
END;

CREATE TRIGGER IF NOT EXISTS trg_tasks_fts_update AFTER UPDATE ON tasks BEGIN
    INSERT INTO tasks_fts(tasks_fts, rowid, title, description, context_md)
    VALUES ('delete', old.rowid, old.title, old.description, old.context_md);
    INSERT INTO tasks_fts(rowid, title, description, context_md)
    VALUES (new.rowid, new.title, new.description, new.context_md);
END;

CREATE TRIGGER IF NOT EXISTS trg_tasks_fts_delete AFTER DELETE ON tasks BEGIN
    INSERT INTO tasks_fts(tasks_fts, rowid, title, description, context_md)
    VALUES ('delete', old.rowid, old.title, old.description, old.context_md);
END;

-- comments
CREATE TRIGGER IF NOT EXISTS trg_comments_fts_insert AFTER INSERT ON comments BEGIN
    INSERT INTO comments_fts(rowid, body_md)
    VALUES (new.rowid, new.body_md);
END;

CREATE TRIGGER IF NOT EXISTS trg_comments_fts_update AFTER UPDATE ON comments BEGIN
    INSERT INTO comments_fts(comments_fts, rowid, body_md)
    VALUES ('delete', old.rowid, old.body_md);
    INSERT INTO comments_fts(rowid, body_md)
    VALUES (new.rowid, new.body_md);
END;

CREATE TRIGGER IF NOT EXISTS trg_comments_fts_delete AFTER DELETE ON comments BEGIN
    INSERT INTO comments_fts(comments_fts, rowid, body_md)
    VALUES ('delete', old.rowid, old.body_md);
END;

-- ----------------------------------------------------------------------------
-- Indexes for backlinks
-- ----------------------------------------------------------------------------

CREATE INDEX IF NOT EXISTS idx_wiki_links_to   ON wiki_links(to_page_id);
CREATE INDEX IF NOT EXISTS idx_wiki_links_from ON wiki_links(from_page_id);