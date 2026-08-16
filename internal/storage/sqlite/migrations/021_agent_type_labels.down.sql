-- Phase 28.19 (reverse): collapse the JSON array back to a scalar.
--
-- LOSSY: agents that carried more than one label lose all but the first.
-- The original column was a single VARCHAR, so any "more than one" row
-- was introduced by this migration — going down discards that added
-- information. The down is intentionally reversible enough to satisfy
-- `orenda migrate down` round-trip tests on synthetic fixtures; for a
-- production instance where the migration has been live long enough that
-- users have set multiple labels, treat down as destructive.
--
-- `json_extract` raises "malformed JSON" on a non-JSON string under
-- modernc.org/sqlite's strict mode; we guard with `json_valid` so the
-- down is idempotent (already-scalar rows survive untouched) and never
-- errors on legacy data that somehow bypassed the up migration.

UPDATE agents
SET type = CASE
  WHEN type = ''            THEN ''                              -- already empty scalar
  WHEN NOT json_valid(type) THEN type                            -- already a scalar
  ELSE COALESCE(json_extract(type, '$[0]'), '')                  -- JSON array → first label
END;
