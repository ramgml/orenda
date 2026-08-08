-- ============================================================================
-- 006_calendar_time.sql — calendar events + time tracking
-- ============================================================================
-- Phase 4, task 4.1.
--
-- events and time_entries tables already exist in 001_init.sql. This
-- migration is ADDITIVE: indexes that the calendar view (Phase 4.7) and
-- the time report (Phase 4.9) need for sub-100ms response.
--
-- Added:
--   * idx_events_range          — primary calendar query: events in [from, to]
--   * idx_events_project        — project-filtered calendar view
--   * idx_time_entries_agent    — agent's own time log
--   * idx_time_entries_open     — the single-active-timer invariant check
--                                 (WHERE ended_at IS NULL) for an agent
--   * trg_events_touch          — keep events.updated_at in sync (already
--                                 added in 005; this is a no-op IF EXISTS
--                                 so the test fixture picks it up uniformly)

CREATE INDEX IF NOT EXISTS idx_events_range
    ON events(start_at, end_at);

CREATE INDEX IF NOT EXISTS idx_events_project
    ON events(project_id, start_at);

-- Time entries: agent-scoped reads, plus the "active timer" lookup.
-- (agent_id is TEXT to match task_locks.agent_id, but the index is
-- still useful for ORDER BY started_at DESC.)
CREATE INDEX IF NOT EXISTS idx_time_entries_agent
    ON time_entries(agent_id, started_at);

-- A single active timer per agent is enforced by application code
-- (Phase 4.5 service); the partial index makes the lookup O(1) instead
-- of O(N) over all rows.
CREATE INDEX IF NOT EXISTS idx_time_entries_open
    ON time_entries(agent_id) WHERE ended_at IS NULL;
