-- Phase 27.8: columns = statuses (machine key).
--
-- The kanban board previously had a `name` column (free text, displayed
-- to the user) but no canonical machine key — the agent flow wrote
-- `tasks.status` directly, never touching `columns.status`, and the
-- two axes drifted apart (Phase 27.7 made the divergence visible: PATCH
-- {status} never moved the card, PATCH {column_id} never changed the
-- label). This migration collapses them: every column carries the
-- status it represents, and the service layer treats the pair as a
-- single invariant `task.status ≡ status(task.column_id)`.
--
-- Backfill rule:
--   1. The five canonical names keep their lowercase form verbatim.
--   2. Custom column names are slugified (lowercase, non-alphanumeric
--      → '_') and de-duplicated within a board by appending '_2',
--      '_3', … so the UNIQUE(board_id, status) we add afterwards
--      holds even for hand-crafted boards.
--
-- The UNIQUE index is created AFTER the backfill so SQLite never
-- sees the intermediate duplicates. Old boards with two columns
-- sharing a name (a pre-27.8 setup mistake) survive with status=
-- 'custom' / 'custom_2'.

ALTER TABLE columns ADD COLUMN status TEXT;

-- Build (board_id, base_slug, dup_n) for every column using a
-- correlated count of earlier rows (lower position, then lower id
-- as a tiebreaker) on the same board with the same base_slug. That
-- count becomes the de-duplication suffix: row 1 keeps the bare
-- slug, row 2+ gets '_2', '_3', …
--
-- Slugify rule: lowercase, replace each non-alphanumeric character
-- with a single '_', then collapse runs of '_' down to one. SQLite
-- only does a single replace pass, so we feed the result through
-- replace('__','_') a couple of times — enough for any plausible
-- name (3+ non-alnum in a row is exceedingly rare).
WITH slugified AS (
  SELECT
    id,
    board_id,
    position,
    CASE
      WHEN name IN ('backlog','todo','in_progress','review','done') THEN name
      ELSE lower(
        replace(replace(replace(replace(replace(replace(replace(replace(name,
          ' ', '_'), '-', '_'), '/', '_'), '.', '_'), ',', '_'),
          '(', ''), ')', ''), '__', '_')
      )
    END AS base_slug
  FROM columns
),
slugified2 AS (
  SELECT id, board_id, position,
    replace(base_slug, '__', '_') AS base_slug
  FROM slugified
),
ranked AS (
  SELECT
    s1.id,
    s1.base_slug,
    (
      SELECT COUNT(*)
      FROM slugified2 s2
      WHERE s2.board_id = s1.board_id
        AND s2.base_slug = s1.base_slug
        AND (
          s2.position < s1.position
          OR (s2.position = s1.position AND s2.id < s1.id)
        )
    ) AS dup_n
  FROM slugified2 s1
)
UPDATE columns
SET status = CASE
  WHEN ranked.dup_n = 0 THEN ranked.base_slug
  ELSE ranked.base_slug || '_' || ranked.dup_n
END
FROM ranked
WHERE columns.id = ranked.id;

CREATE UNIQUE INDEX idx_columns_board_status
  ON columns(board_id, status);
