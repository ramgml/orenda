-- Phase 28.19: agents.type as free-form label set.
--
-- The `agents.type` column used to be a single VARCHAR drawn from a fixed
-- list ('qwen' | 'claude' | 'custom'). Phase 28.19 lifts that constraint:
-- the column becomes a JSON array of arbitrary, normalised labels (trim,
-- lowercase, dedupe, sort). The list is short and operator-curated, so a
-- proper catalog/join table is overkill — we reuse the same "TEXT column
-- carrying a JSON array" convention as `bot_subscriptions.events`.
--
-- The schema itself does not change (the column was already TEXT); we
-- only backfill existing rows:
--   ''           → '[]'                                  (safe empty default)
--   'qwen'       → '["qwen"]'                            (single string → singleton)
--   anything     → json_array(type)                       (defensive, never NULL)
--
-- After this migration the column invariant is: a JSON array of strings,
-- length >= 0, lowercased and sorted. The Go-side Validate() applies the
-- same normalisation at write time, so the database stays consistent
-- regardless of caller.

UPDATE agents
SET type = CASE
  WHEN type = ''           THEN '[]'
  WHEN json_valid(type)    THEN type                     -- already JSON (idempotent re-run)
  ELSE json_array(type)
END
WHERE type IS NOT NULL;
