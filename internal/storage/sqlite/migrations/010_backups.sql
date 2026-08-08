-- ============================================================================
-- 010_backups.sql — backup_settings + backup_log indexes
-- ============================================================================
-- Phase 7, task 7.1.
--
-- backup_settings (key/value) and backup_log already exist in 001_init.sql.
-- This migration adds an index to speed the backup log list view.

CREATE INDEX IF NOT EXISTS idx_backup_log_type_created
    ON backup_log(type, created_at);

-- backup_settings is a (key, value JSON) table queried by exact key only;
-- no secondary index needed beyond the PK.