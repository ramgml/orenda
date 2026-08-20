-- ============================================================================
-- 025_task_retracted.sql — survive the task row on agent retract (Phase 33.2.1)
-- ============================================================================
-- Phase 33.2.1: the original Phase 33.2 RetractProposal path wrote a
-- task.deleted activity row, then deleted the task. Because
-- task_activity.task_id carries REFERENCES tasks(id) ON DELETE
-- CASCADE, the activity row vanished with the task — there was no
-- audit trail of who retracted what (the security review caught
-- this). The WS event is ephemeral and doesn't survive a restart
-- either.
--
-- This migration adds a dedicated tombstone table that survives the
-- task row:
--
--   - task_id has NO FK to tasks — a retracted task may be deleted;
--     the tombstone keeps the snapshot for audit.
--   - snapshot_json carries the pre-delete task body so the owner
--     can see exactly what was proposed and by whom.
--   - The "id" primary key is a fresh UUID — a retract is a
--     permanent audit event, not a row in tasks.
--
-- We keep this table deliberately narrow: id, task_id, snapshot_json,
-- actor_type, actor_id, retracted_at. We do NOT index on task_id
-- because retract lookups always go through the WS hub for the
-- status-quo; the audit-pull surface (admin tools, future tasks) can
-- add an index when it lands.
CREATE TABLE task_retracted (
    id              TEXT PRIMARY KEY,
    task_id         TEXT NOT NULL,
    snapshot_json   TEXT NOT NULL,
    actor_type      TEXT NOT NULL,
    actor_id        TEXT NOT NULL,
    retracted_at    TEXT NOT NULL DEFAULT (datetime('now'))
);
