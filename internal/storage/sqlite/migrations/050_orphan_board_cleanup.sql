-- ============================================================================
-- 050_orphan_board_cleanup.sql — drop boards (and their columns) whose
-- project row no longer exists
-- ============================================================================
-- T369. Migration 015 removed the legacy system-Inbox project
-- (00000000-0000-0000-0000-00000000cafe) while running under
-- `PRAGMA foreign_keys = OFF` (its `-- orenda:foreign_keys_off` marker).
-- With foreign keys OFF SQLite does not fire ON DELETE CASCADE, so
-- 015's step 8 (`DELETE FROM projects WHERE id = '...cafe'`) silently
-- orphaned the board the pre-Phase-16 runtime bootstrap
-- (ensureInboxBoardAndColumns) had seeded for the Inbox — along with
-- that board's columns. 015's own safety net could not see the damage:
-- it ran `PRAGMA foreign_key_check` through ExecContext, which discards
-- result rows, so a non-empty violation report looks like success.
--
-- The orphan is unreachable through the app (its project page and board
-- route are gone) but `PRAGMA foreign_key_check` reports it forever,
-- it bakes into every snapshot, and `backup restore` verify honestly
-- fails on such databases.
--
-- Fresh installs are unaffected (015 runs with no boards to orphan):
-- this migration only repairs databases upgraded across the Phase-16
-- boundary while the legacy bootstrap was still seeding the Inbox board.
--
-- No stub project is resurrected on purpose: 015 deliberately removed
-- the fake Inbox project, and a board without a project has no meaning
-- in the data model. Deleting the orphan rows is data hygiene, not
-- data loss.
--
-- Runs on the normal foreign_keys=ON path: plain DELETEs cannot create
-- new violations, tasks.column_id follows dropped columns via
-- ON DELETE SET NULL, and any real violation would abort the migration
-- transaction.

-- Columns of orphan boards go first, explicitly (their own FK would
-- cascade them, but being explicit keeps the intent reviewable).
DELETE FROM columns
WHERE board_id IN (
    SELECT id FROM boards
    WHERE project_id NOT IN (SELECT id FROM projects)
);

-- Boards whose project is gone.
DELETE FROM boards
WHERE project_id NOT IN (SELECT id FROM projects);
