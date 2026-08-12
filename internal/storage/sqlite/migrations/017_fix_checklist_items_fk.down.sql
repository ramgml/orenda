-- ============================================================================
-- 017_fix_checklist_items_fk.down.sql — rebuild with the legacy FK
-- ============================================================================
-- Mirror of 017_fix_checklist_items_fk. We re-attach checklist_items
-- to the dropped table (the bug the up migration fixed). Because
-- the FK target no longer exists, the rebuild lets FK constraints
-- be dropped silently by SQLite — at the end of the down we have
-- the same broken state as just before 017 ran.
--
-- orenda:foreign_keys_off — the rename below breaks any active FK;
-- we must run with FK enforcement off so the drops succeed.

ALTER TABLE checklist_items RENAME TO checklist_items_v2;
ALTER TABLE checklists RENAME TO checklists_old;

CREATE TABLE checklists (
    id              TEXT PRIMARY KEY,
    task_id         TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    title           TEXT NOT NULL,
    position        INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX idx_checklists_task ON checklists(task_id);

INSERT INTO checklists (id, task_id, title, position)
SELECT id, task_id, title, position FROM checklists_old;

CREATE TABLE checklist_items (
    id              TEXT PRIMARY KEY,
    checklist_id    TEXT REFERENCES checklists_old(id) ON DELETE CASCADE,
    title           TEXT NOT NULL,
    done            INTEGER NOT NULL DEFAULT 0,
    position        INTEGER NOT NULL DEFAULT 0
);
INSERT INTO checklist_items (id, checklist_id, title, done, position, rowid)
SELECT id, checklist_id, title, done, position, rowid FROM checklist_items_v2;

DROP TABLE checklist_items_v2;
DROP TABLE checklists_old;
