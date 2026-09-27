-- ============================================================================
-- 001_baseline.down.sql — drop the PostgreSQL baseline schema (T361)
-- ============================================================================
-- Reverse of 001_baseline.up.sql: drop every table in reverse creation
-- order (children before parents, so no CASCADE is needed). Indexes and
-- the touch triggers die with their tables; the shared trigger function
-- is dropped last, once nothing references it. The schema_migrations
-- bookkeeping table is the runner's property and is intentionally left
-- alone.

DROP TABLE IF EXISTS lesson_number_seq;
DROP TABLE IF EXISTS course_number_seq;
DROP TABLE IF EXISTS wiki_page_number_seq;
DROP TABLE IF EXISTS project_number_seq;
DROP TABLE IF EXISTS task_number_seq;
DROP TABLE IF EXISTS lesson_reviews;
DROP TABLE IF EXISTS tutor_messages;
DROP TABLE IF EXISTS project_agents;
DROP TABLE IF EXISTS wiki_blocks;
DROP TABLE IF EXISTS chat_threads;
DROP TABLE IF EXISTS chat_messages;
DROP TABLE IF EXISTS task_retracted;
DROP TABLE IF EXISTS project_activity;
DROP TABLE IF EXISTS course_activity;
DROP TABLE IF EXISTS study_proposals;
DROP TABLE IF EXISTS course_quizzes;
DROP TABLE IF EXISTS course_lessons;
DROP TABLE IF EXISTS course_modules;
DROP TABLE IF EXISTS task_dependencies;
DROP TABLE IF EXISTS time_entries;
DROP TABLE IF EXISTS task_activity;
DROP TABLE IF EXISTS task_tags;
DROP TABLE IF EXISTS checklist_items;
DROP TABLE IF EXISTS checklists;
DROP TABLE IF EXISTS task_locks;
-- tasks ⇄ courses reference each other (generator_task_id vs
-- study_course_id): a single DROP resolves the pair's mutual dependency.
DROP TABLE IF EXISTS tasks, courses;
DROP TABLE IF EXISTS sync_ops;
DROP TABLE IF EXISTS backup_log;
DROP TABLE IF EXISTS backup_settings;
DROP TABLE IF EXISTS bot_subscriptions;
DROP TABLE IF EXISTS notifications;
DROP TABLE IF EXISTS wiki_links;
DROP TABLE IF EXISTS attachments;
DROP TABLE IF EXISTS mentions;
DROP TABLE IF EXISTS comments;
DROP TABLE IF EXISTS tags;
DROP TABLE IF EXISTS columns;
DROP TABLE IF EXISTS boards;
DROP TABLE IF EXISTS projects;
DROP TABLE IF EXISTS wiki_pages;
DROP TABLE IF EXISTS agents;
DROP TABLE IF EXISTS api_tokens;
DROP TABLE IF EXISTS users;

DROP FUNCTION IF EXISTS orenda_touch_updated_at();
