-- ============================================================================
-- 017_fix_checklist_items_fk.sql
-- ============================================================================
-- Hotfix for a latent bug in migration 015: the rename
-- `checklists → checklists_old` + rebuild left the FK on
-- `checklist_items.checklist_id` pointing at the dropped
-- `checklists_old` table. SQLite silently rewrites FK references
-- on rename when `legacy_alter_table=ON` (the default), so the
-- `INSERT INTO checklist_items` path hits 'no such table:
-- main.checklists_old' and fails.
--
-- The cleanest fix: rebuild checklist_items so its FK references
-- the fresh `checklists` table. We copy the rows (so existing
-- checklists aren't lost) and drop the old table.
--
-- Note: there's no history of this being observed in production —
-- we hit it while writing Phase 17 storage tests. Migration 015
-- shipped without anyone ever inserting a checklist item, so the
-- FK was silently broken from day one.

ALTER TABLE checklist_items RENAME TO checklist_items_old;
CREATE TABLE checklist_items (
    id              TEXT PRIMARY KEY,
    checklist_id    TEXT NOT NULL REFERENCES checklists(id) ON DELETE CASCADE,
    title           TEXT NOT NULL,
    done            INTEGER NOT NULL DEFAULT 0,
    position        INTEGER NOT NULL DEFAULT 0
);
INSERT INTO checklist_items (id, checklist_id, title, done, position, rowid)
SELECT id, checklist_id, title, done, position, rowid FROM checklist_items_old;
DROP TABLE checklist_items_old;
