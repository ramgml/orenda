-- Migration 051 (down) — drop the canonical `rejected` parking columns
-- (Task 376). Best-effort reverse: deletes the columns the up-side
-- would have inserted (name = machine key = 'rejected'). A pre-051
-- Phase 27.8 custom column that was later RENAMED survives (name no
-- longer matches); one still named 'rejected' is deleted too — the
-- up-side NOT EXISTS guard cannot distinguish it from ours. Tasks are
-- untouched — dropping a column orphans no row (the app layer
-- re-resolves the column on the next move).
--
-- The Go-side DefaultColumns still lists `rejected` after this down, so
-- freshly created projects keep the column until the app version is
-- rolled back too.

DELETE FROM columns WHERE status = 'rejected' AND name = 'rejected';
