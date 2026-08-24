ALTER TABLE wiki_pages ADD COLUMN content_format TEXT NOT NULL DEFAULT 'markdown';

CREATE TABLE wiki_blocks (
    id              TEXT PRIMARY KEY,
    page_id         TEXT NOT NULL REFERENCES wiki_pages(id) ON DELETE CASCADE,
    parent_block_id TEXT REFERENCES wiki_blocks(id) ON DELETE CASCADE,
    position        INTEGER NOT NULL DEFAULT 0,
    type            TEXT NOT NULL,
    data            TEXT NOT NULL DEFAULT '{}',
    created_at      TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at      TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX idx_wiki_blocks_page ON wiki_blocks(page_id, parent_block_id, position);
