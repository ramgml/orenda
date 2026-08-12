-- ============================================================================
-- 006_calendar_time.down.sql — drop events + time_entries indexes
-- ============================================================================

DROP INDEX IF EXISTS idx_events_range;
DROP INDEX IF EXISTS idx_events_project;
DROP INDEX IF EXISTS idx_time_entries_agent;
DROP INDEX IF EXISTS idx_time_entries_open;
