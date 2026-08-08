# Orenda — Database Schema

SQLite (WAL mode), migrations in `internal/storage/sqlite/migrations/`.

## Core

```text
users              id · email (uniq) · password_hash · display_name · role · created_at · updated_at
api_tokens         id · user_id →users · name · hash · scopes · last_used_at · expires_at · created_at
agents             id · name (uniq) · type · description · token_id →api_tokens · last_seen_at · status · max_concurrent · created_at

projects           id · name · color · description · owner_id →users · archived · created_at · updated_at
boards             id · project_id →projects · name · position · created_at
columns            id · board_id →boards · name · position · wip_limit · color
tags               id · name (uniq) · color

tasks              id · project_id →projects · parent_task_id →tasks · column_id →columns
                   title · description · status · priority
                   assignee_type · assignee_id · awaiting
                   context_md · agent_notes
                   due_at · started_at · claimed_at · completed_at
                   time_estimate_s · time_spent_s · position · created_at · updated_at
task_locks         task_id (PK) →tasks · agent_id →agents · acquired_at     — atomic claim primitive
subtasks           id · task_id →tasks · title · done · position
checklists         id · task_id →tasks · title · position
checklist_items    id · checklist_id →checklists · title · done · position
task_tags          task_id →tasks · tag_id →tags · PK(task_id, tag_id)
```

## Collaboration

```text
comments           id · target_type · target_id · author_type · author_id · body_md · created_at
mentions           comment_id →comments · target_type · target_id · PK(...)
attachments        id · target_type · target_id · filename · mime · size · path · sha256 · uploaded_by_* · created_at
task_activity      id · task_id →tasks · actor_type · actor_id · action · payload · created_at
time_entries       id · task_id →tasks · agent_id · started_at · ended_at · duration_s · source
```

## Calendar / Wiki / Notifications / Backup

```text
events             id · title · description · start_at · end_at · all_day · color · project_id →projects
                   recurrence_rule · parent_event_id →events · created_at · updated_at

wiki_pages         id · parent_id →wiki_pages · slug (uniq) · title · content_md · position · created_at · updated_at
wiki_links         from_page_id →wiki_pages · to_page_id →wiki_pages · PK(...)

notifications      id · user_id →users · type · target_* · payload · read_at · dedup_key (uniq) · created_at
bot_subscriptions  id · user_id →users · bot_type · target_address · events (JSON) · enabled · created_at

backup_settings    key (PK) · value (JSON)
backup_log         id · type · status · message · snapshot_path · created_at
sync_ops           client_id (PK) · server_id · op · target · applied_at
```

## FTS5 (migration 008)

```text
pages_fts      (title, content_md)       content=wiki_pages
tasks_fts      (title, description, context_md)  content=tasks
comments_fts   (body_md)                 content=comments
```

All three use `unicode61 remove_diacritics 2` (Cyrillic-safe) and are kept in
sync via INSERT/UPDATE/DELETE triggers.

## Migrations

| File | Adds |
|---|---|
| 001_init.sql | full schema (17 tables) |
| 002_auth.sql | idx_api_tokens_hash, idx_users_email, trg_users_touch |
| 003_projects_tasks.sql | composite task indexes + touch triggers |
| 004_agents.sql | agent status/last_seen/task_locks indexes |
| 005_comments_attachments.sql | comment/attachment/activity indexes + wiki/events triggers |
| 006_calendar_time.sql | events range + time_entries indexes (incl. partial `ended_at IS NULL`) |
| 007_time_entries_actor.sql | drop FK on time_entries.agent_id (recreate table) — lets users track time |
| 008_wiki.sql | FTS5 tables + sync triggers + wiki_links indexes |
| 009_notifications.sql | unread/target/subs indexes |
| 010_backups.sql | backup_log(type, created_at) index |
| 011_sync_ops.sql | sync_ops idempotency table |
