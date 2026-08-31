-- Migration 041 — comment edit timestamps (Task 112).
--
-- Before: comments was immutable — the UI had no way to tell an
-- edited comment from an original one and there was no audit trail
-- of the edit.
--
-- After:
-- - edited_at TEXT NULL on comments. Set by the repository layer
--   when Service.Update overwrites body_md. NULL = never edited.
-- - No backfill: comments predating this migration have no edit
--   history, so NULL is the truthful value for them.
-- - The full router exposes PATCH on both the user route
--   (/api/v1/tasks/{id}/comments/{commentId}) and the agent route
--   (/api/v1/agent/tasks/{id}/comments/{commentId}).

ALTER TABLE comments ADD COLUMN edited_at TEXT;
