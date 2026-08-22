-- Reverse 037_wiki_page_numbers.sql

DROP INDEX IF EXISTS idx_wiki_pages_number;
DROP TABLE IF EXISTS wiki_page_number_seq;
ALTER TABLE wiki_pages DROP COLUMN number;
