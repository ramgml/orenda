-- ============================================================================
-- 002_auth.down.sql — drop indexes and trigger added by 002_auth.up.sql
-- ============================================================================
-- Reverses the additive changes from 002_auth. The users and api_tokens
-- tables themselves live in 001_init.sql; their DROP belongs there.

DROP INDEX IF EXISTS idx_api_tokens_hash;
DROP INDEX IF EXISTS idx_users_email;
DROP TRIGGER IF EXISTS trg_users_touch;
