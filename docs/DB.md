# Orenda — Database Schema

SQLite (WAL mode), migrations in `internal/storage/sqlite/migrations/`.
Current version: **021_agent_type_labels** (020 up files; номер 018 не занят).

## Core

```text
users              id · email (uniq) · password_hash · display_name · role · created_at · updated_at
api_tokens         id · user_id →users · name · hash · scopes · last_used_at · expires_at · created_at
agents             id · name (uniq) · type (JSON array of free-form labels, 021) · description
                   · token_id →api_tokens · last_seen_at · status · max_concurrent · created_at

projects           id · name · color · description · owner_id →users · archived · created_at · updated_at
boards             id · project_id →projects · name · position · created_at
columns            id · board_id →boards · name · position · wip_limit · color
                   · status (020) — machine key; UNIQUE(board_id, status); backfilled from name
tags               id · name (uniq) · color

tasks              id · project_id →projects (NULL = Inbox, since 015) · parent_task_id →tasks · column_id →columns
                   title · description · status · priority
                   assignee_type · assignee_id · awaiting
                   context_md · agent_notes
                   due_at · started_at · claimed_at · completed_at
                   start_at · end_at · all_day · color (012 — calendar on tasks) · recurrence (015)
                   time_estimate_s · time_spent_s · position · created_at · updated_at
task_locks         task_id (PK) →tasks · agent_id →agents · acquired_at     — atomic claim primitive
checklists         id · task_id →tasks · title · position
checklist_items    id · checklist_id →checklists · title · done · position  (FK fixed in 017)
task_tags          task_id →tasks · tag_id →tags · PK(task_id, tag_id)
task_dependencies  task_id →tasks · depends_on_id →tasks · PK(task_id, depends_on_id) (016)
```

Dropped along the way: `subtasks` (013 — child tasks are `tasks` rows with
`parent_task_id`), `events` (012 — calendar lives on `tasks`).

## Collaboration

```text
comments           id · target_type · target_id · author_type · author_id · body_md · created_at
mentions           comment_id →comments · target_type · target_id · PK(...)
attachments        id · target_type · target_id · filename · mime · size · path · sha256 · uploaded_by_* · created_at
task_activity      id · task_id →tasks · actor_type · actor_id · action · payload · created_at
course_activity    id · course_id →courses · actor_type · actor_id · kind · payload · created_at
time_entries       id · task_id →tasks · agent_id · started_at · ended_at · duration_s · source
```

## Wiki / Notifications / Backup / Sync

```text
wiki_pages         id · parent_id →wiki_pages · slug (uniq) · title · content_md · position · created_at · updated_at
wiki_links         from_page_id →wiki_pages · to_page_id →wiki_pages · PK(...)

notifications      id · user_id →users · type · target_* · payload · read_at · dedup_key (uniq) · created_at
bot_subscriptions  id · user_id →users · bot_type · target_address · events (JSON) · enabled · created_at

backup_settings    key (PK) · value (JSON)
backup_log         id · type · status · message · snapshot_path · created_at
sync_ops           client_id (PK) · server_id · op · target · applied_at
```

## Courses (LMS, migration 019, pace_notes on 022)

```text
courses            id · title · intent_md · level · pace · status (draft|review|active|done|archived)
                   · owner_id →users · generator_task_id →tasks (NULL) · pace_notes_md (Phase 31, default '')
                   · created_at · updated_at
course_modules     id · course_id →courses CASCADE · title · description · position
course_lessons     id · module_id →modules CASCADE · title · content_md · status (locked|open|done) · position
                   · task_id →tasks SET NULL
course_quizzes     id · lesson_id →lessons CASCADE · position · question_md · expected_md · kind (open|exact)
```

`pace_notes_md` (Phase 31) is the agent-planner's read signal and the user's
free-form scratchpad for "how should the course be paced?". Trim + ≤ 64 KiB
enforced by `course.Course.Validate`; the repo's `UpdatePaceNotesMD` is the
narrow PATCH the agent uses (no title/status noise).

## Study reminders (Phase 31, migration 022)

A study reminder is an inbox task with a non-null `study_course_id` linking
to the course. The reminder survives the course (FK SET NULL on `tasks`),
and the course CASCADEs `study_proposals` when removed.

```text
tasks.study_course_id    TEXT →courses(id) ON DELETE SET NULL  · partial idx_tasks_study_course
study_proposals          id · course_id →courses CASCADE (NULL allowed) · title · body_md
                         · target_date (YYYY-MM-DD) · status (pending|accepted|dismissed)
                         · created_by_agent →agents(id) · accepted_task_id →tasks(id) SET NULL
                         · created_at · resolved_at
```

The lifecycle: pending (visible in tray) → accept → inbox task (materialises
the reminder with `due_at = max(target_date, today)`) or dismiss. Mark* methods
are idempotent — see `study.MarkAccepted` / `MarkDismissed`.

## FTS5 (migration 008)

```text
pages_fts      (title, content_md)               content=wiki_pages
tasks_fts      (title, description, context_md)  content=tasks
comments_fts   (body_md)                         content=comments
```

All three use `unicode61 remove_diacritics 2` (Cyrillic-safe) and are kept in
sync via INSERT/UPDATE/DELETE triggers.

## Down-migrations

Every up-file `NNN_*.sql` has a paired `NNN_*.down.sql`. The custom runner
(`internal/storage/sqlite/db.go::MigrateDown`; CLI `orenda migrate down`)
rolls back one version per invocation. Header markers change runner behaviour:

- `-- orenda:irreversible[: <reason>]` — down returns `ErrMigrationIrreversible`
  (currently: 001, 013, 015 — rebuilds/data moves that can't be undone safely).
- `-- orenda:foreign_keys_off` — run with FK enforcement off (table rebuilds,
  cascades that would otherwise fail mid-drop).

## Migrations

| File | Adds |
|---|---|
| 001_init.sql | full schema (25 tables) |
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
| 012_events_to_tasks.sql | calendar on tasks (start_at/end_at/all_day/color) + idx_tasks_time; drops `events` |
| 013_subtasks_to_children.sql | subtasks → tasks.parent_task_id; drops `subtasks` |
| 014_child_tasks_inherit_column.sql | child tasks default to the parent's column |
| 015_inbox_no_project.sql | tasks.project_id nullable (Inbox = no project); tasks.recurrence; table rebuild |
| 016_task_dependencies.sql | task_dependencies + both FK indexes |
| 017_fix_checklist_items_fk.sql | checklist_items FK pointed at dropped `checklists` — re-pointed |
| 019_courses.sql | courses / course_modules / course_lessons / course_quizzes + FK indexes |
| 020_columns_status.sql | columns.status machine key (backfill from name, slug for customs) + UNIQUE(board_id, status) |
| 021_agent_type_labels.sql | agents.type backfill (scalar → JSON-array); idempotent on re-run; down is lossy on multi-label rows |
| 022_study_planning.sql | courses.pace_notes_md (default '') · tasks.study_course_id (FK SET NULL) + partial idx · study_proposals |

*(номер 018 пропущен — зарезервированная нумерация съехала от текста фаз; не используется)*
