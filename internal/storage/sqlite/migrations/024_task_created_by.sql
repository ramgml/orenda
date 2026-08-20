-- ============================================================================
-- 024_task_created_by.sql — track who created a task (Phase 33.2)
-- ============================================================================
-- Phase 33.2: the agent must be able to manage its own proposals and the
-- holder of a task must be able to update `agent_notes`. Both flows need
-- to identify "the line of authorship" on a task row: the agent's
-- self-service permission is gated on `created_by_type='agent' AND
-- created_by_id=me`, and the existing user-side create needs to keep
-- working with the same gate flipped to `user`.
--
-- Before this migration the schema carried no creator info — the
-- relationship was implicit ("if it's in the past 5 minutes, the
-- caller probably created it"). That meant:
--   * an agent could not safely edit its own proposal after the
--     owner's request shifted the spec (Phase 33.2 fixed this);
--   * the activity timeline could not differentiate "user created"
--     from "agent created" without a side-by-side with the
--     task.created row (which a later rename or migration could
--     lose).
--
-- Design:
--   * created_by_type: 'user' | 'agent' — category, not a polymorphic FK.
--     Same convention as `assignee_type` (Task.AssigneeType).
--   * created_by_id: the user.id or agent.id. NULL is the legacy case
--     (rows that existed before this migration); the PATCH/DELETE
--     guard treats legacy rows as user-authored (the owner has implicit
--     ownership of those). The `created_by_type='user'` + NULL id
--     shape is never produced by the Go side — Validate() rejects it.
--   * Add a NOT NULL DEFAULT 'user' on created_by_type so the
--     existing insert path (user-side POST /api/v1/tasks) keeps
--     working without a code change. Existing rows are backfilled
--     with the same default.
--   * Add created_by_id with no NOT NULL: legacy rows have no id
--     and the agent-side edit/withdraw logic treats NULL id as
--     "not my row" (the proposal is owned by the human user, not
--     my agent identity).
--   * Add an index on `created_by_type, created_by_id` so the
--     permission gate ("fetch my own proposals") is fast when the
--     backlog grows.
--
-- Down migration drops the index and the columns. We do not
-- allow `MigrateDown` to fail loud on legacy data loss — the
-- `deleted` semantic on this migration is "lose the creator
-- attribution", which is recoverable only if the activity row
-- has been preserved (it has; task_activity carries actor_type /
-- actor_id for every task.created event).

ALTER TABLE tasks ADD COLUMN created_by_type TEXT NOT NULL DEFAULT 'user';
ALTER TABLE tasks ADD COLUMN created_by_id   TEXT;

CREATE INDEX idx_tasks_created_by ON tasks(created_by_type, created_by_id)
    WHERE created_by_id IS NOT NULL;
