-- ============================================================================
-- 037_wiki_page_numbers.sql — human-readable sequential wiki page numbers
-- ============================================================================
-- Every wiki page gets a small monotonically-increasing integer (`number`)
-- alongside its UUID. Agents and humans reference pages as "W42" in
-- conversation; the agent surface (REST /api/v1/agent/pages/{slug},
-- MCP orenda_pages_get) accepts either form and resolves "W42" / "w42"
-- through this column.
--
-- Assignment rule for NEW rows: wikiRepo.Create draws the next number
-- from the `wiki_page_number_seq` high-watermark (UPDATE ... RETURNING in
-- the same transaction as the INSERT). A bare COALESCE(MAX(number),0)+1
-- over `wiki_pages` would REUSE a number after the newest page is deleted —
-- a "W42" reference in a commit message or branch name has to keep pointing
-- at the same page forever, so the watermark never moves backwards. Numbers
-- are NEVER reused after a delete.
--
-- The three-step shape mirrors 033_task_numbers.sql exactly:
--   1. ADD COLUMN with DEFAULT 0 (SQLite requires a constant default)
--   2. Backfill via ROW_NUMBER() OVER (ORDER BY created_at, rowid)
--   3. UNIQUE index after backfill (transient 0s never collide)
-- The whole body runs in one transaction (runner default).

ALTER TABLE wiki_pages ADD COLUMN number INTEGER NOT NULL DEFAULT 0;

UPDATE wiki_pages SET number = (
    SELECT numbered.rn FROM (
        SELECT id, ROW_NUMBER() OVER (ORDER BY created_at, rowid) AS rn
        FROM wiki_pages
    ) AS numbered
    WHERE numbered.id = wiki_pages.id
);

CREATE UNIQUE INDEX idx_wiki_pages_number ON wiki_pages(number);

-- High-watermark for new assignments. One row, id pinned to 1;
-- `next` is the number the following Create will draw. Seeded past
-- the backfill so existing numbers are never re-issued.
CREATE TABLE wiki_page_number_seq (
    id   INTEGER PRIMARY KEY CHECK (id = 1),
    next INTEGER NOT NULL
);
INSERT INTO wiki_page_number_seq (id, next)
SELECT 1, COALESCE(MAX(number), 0) + 1 FROM wiki_pages;
