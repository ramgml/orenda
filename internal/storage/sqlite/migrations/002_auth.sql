-- ============================================================================
-- 002_auth.sql — auth-related indexes and constraints
-- ============================================================================
-- Phase 1, task 1.1.
--
-- NOTE: users and api_tokens tables were created in 001_init.sql (the full
-- schema was shipped in one migration per Phase 0 decision; see SESSION.md).
-- This migration is therefore ADDITIVE — it only adds indexes and constraints
-- on top of the existing tables.
--
-- We add:
--   * idx_api_tokens_hash — fast lookup on every authenticated request
--     (bcrypt comparison is expensive; the index narrows to one row).
--   * idx_users_email — UNIQUE on email already creates an implicit index,
--     but a named one is useful for EXPLAIN and explicit query plans.
--   * trg_users_touch — keep updated_at in sync on UPDATE.

CREATE INDEX IF NOT EXISTS idx_api_tokens_hash ON api_tokens(hash);

CREATE INDEX IF NOT EXISTS idx_users_email ON users(email);

-- updated_at trigger on users: every UPDATE bumps updated_at to now.
CREATE TRIGGER IF NOT EXISTS trg_users_touch
AFTER UPDATE ON users
FOR EACH ROW
BEGIN
    UPDATE users SET updated_at = datetime('now') WHERE id = OLD.id;
END;

-- updated_at trigger on api_tokens: bumps last_used_at on token-use UPDATEs
-- is intentionally NOT done here — last_used_at is written explicitly by the
-- auth middleware so it can be skipped (e.g. health probes).