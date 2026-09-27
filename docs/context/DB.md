# Orenda — Database Schema

Two storage drivers behind one repository seam
([README → Database backends](../README.md#database-backends-sqlite-or-postgresql)):
**SQLite** (WAL mode, default) and **PostgreSQL** (opt-in,
`storage.driver=postgres` — embedded / external server / Docker).
Driver configuration (yaml + the `ORENDA_STORAGE__*` env table) lives in
the README; this document describes what each driver actually runs.

## SQLite

Migrations in `internal/storage/sqlite/migrations/`.
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
rolls back one version per invocation. The CLI reads the database raw
before rolling back: `migrate down` and `migrate status` do NOT apply
pending migrations first (a previous version ran a hidden `Migrate(UP)`
in its open path, which re-applied whatever a previous `down` had just
rolled back — T154). `MigrateDown` bootstraps `schema_migrations` itself
when the table is missing. Header markers change runner behaviour:

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

## Configuration reference

The full `storage:` yaml block and the `ORENDA_STORAGE__*` env table live in
[README → Database backends](../README.md#database-backends-sqlite-or-postgresql)
(single source of truth — do not copy the table here). Rules worth knowing:

- `storage.postgres.dsn` **wins outright** over
  `host/port/user/password/database/ssl_mode` (T363). A DSN is never echoed —
  logs carry `host:port/database` only (`postgres.TargetDescription`).
- `storage.driver=postgres` requires `storage.postgres.database` (embedded
  included) or a `dsn`; anything else fails at config validation, not at
  first query.
- `storage.busy_timeout_ms` maps to a session `lock_timeout` on postgres and
  keeps meaning `busy_timeout` on sqlite.

## PostgreSQL storage driver

The postgres leg (T360–T367, [wiki:storage-adapters]) does not fork the
repository layer: the same `sqlite.New*Repository` constructors run through
the dialect shim (`internal/storage/shim` — rebinds `?` → `$n`, rewrites
`datetime('now')` and `INSERT OR IGNORE`-class SQL), pgx v5 stdlib connector
(`internal/storage/postgres/runtime.go`), and neutral error sentinels —
SQLSTATE `23505`/`23503` classify into the same unique/FK violations sqlite
text-classifies.

### Migrations

`internal/storage/postgres/migrations/` — a *baseline*, not a replay:

| File | Contents |
|---|---|
| `001_baseline` | consolidated final state of the sqlite chain `001_init`…`049_chat_messages_user_idx` (018 does not exist upstream). Data backfills/table rebuilds from the sqlite chain are state-neutral on a fresh database and are not replayed. |
| `002_search` | full-text objects: generated `tsvector` columns + GIN (no sync triggers — `GENERATED ALWAYS … STORED` maintains itself; sqlite needs the 9 FTS5 triggers) |

`orenda migrate up/down/status` work identically on both drivers — the
postgres runner applies each file inside a transaction (transactional DDL)
and records versions in its own `schema_migrations(version, applied_at)`;
every up-file has a paired `.down.sql` (001's down is lossy-by-design, same
policy as sqlite).

### Type mapping (D3: mirror sqlite)

TEXT timestamps (same `'YYYY-MM-DD HH:MM:SS'` / RFC3339 formats —
`DEFAULT to_char(now() AT TIME ZONE 'UTC', …)` reproduces
sqlite's `datetime('now')`), INTEGER booleans, TEXT UUIDs,
`DOUBLE PRECISION` for sqlite REAL. `updated_at` touch triggers hang off a
shared `orenda_touch_updated_at()` function. `mentions` gets an explicit
identity column in place of sqlite's implicit `rowid`. Not on the table
today: `timestamptz`/`boolean` — a possible future migration, not a
compatibility requirement.

### Full-text search

Same domain contract, engine-native ranking: `phraseto_tsquery('simple', ?)`
mirrors the sqlite phrase wrapper (no stemming, no stop-words — parity with
`unicode61`), `ts_headline` reproduces the `<mark>`/`</mark>` snippet
markers (~30-token window), `ORDER BY ts_rank DESC` mirrors `ORDER BY -bm25`.
**Known difference (PR #266):** the ranking *numbers* are not comparable
across engines — bm25 is IDF + length-normalized, `ts_rank` is plain term
frequency; hit sets and sanity ordering match. Neither engine stems:
«поиск» never matches «поиска» on either driver. Russian stemming
(`'russian'` config) is deliberately out of scope.

### Embedded runtime (zero-setup mode)

`storage.postgres.embedded=true` makes the binary own a PostgreSQL 16
cluster ([fergusstrange/embedded-postgres], zonky binaries from Maven
Central, cached in `~/.embedded-postgres-go`; offline mirrors via
`storage.postgres.binaries_url`):

- lifecycle: `serve`, `migrate` and `user` commands start it before any DB
  access; shutdown/command exit stops it (clean postmaster, no orphans);
- `PGDATA` is `<data_dir>/postgres/` and survives restarts (initdb runs
  once); the runtime scratch dir lives *outside* PGDATA and is wiped per
  start — never nest them;
- loopback-only `127.0.0.1:5433` (`embedded_port`), bootstrap user/password
  `postgres`/`postgres`, `sslmode=disable` (initdb creates no certificates);
- locale pinned to `C.UTF-8` (fallback `en_US.UTF-8`), encoding `UTF8` — a
  C-ctype cluster is rejected loudly after start (case-folding search would
  silently break). Cross-platform locale policy: T368.

### Backup, restore, maintenance (T366)

Snapshots on postgres are `pg_dump --format=custom` archives in the same
`SnapshotDir` with the same naming/rotation as sqlite; the mirror `LATEST`
artifact becomes `orenda-LATEST.dump`. `orenda backup restore` restores
into a scratch database on the target server (must be up — `pg_dump` owns
the connection), verifies `pg_restore --list` + applied migrations, drops
the scratch; `--to <database>` keeps it for promotion. Maintenance "verify"
is per-dialect (sqlite pragmas vs scratch restore). **Honest tooling
contract:** the embedded bundle ships *server* binaries only — `pg_dump`/
`pg_restore` resolve via `storage.postgres.dump_bin` (bare name → `PATH`,
path → as-is; `pg_restore` prefers the resolved `pg_dump`'s directory) →
extracted embedded-runtime dirs → `PATH` (install `postgresql-client` in
practice). The error says exactly this when a tool is missing — it never
promises an embedded dump.

### Known limitations

- No automatic sqlite → postgres data migration: switching an existing
  install is an open owner decision (a `pg_restore` cannot read sqlite).
- `rowid`-dependent code paths were replaced explicitly (identity columns);
  text `ORDER BY` collation differs between engines — key list orders are
  pinned by the two-driver test matrix (T364), not by collation luck.
- Embedded-mode dump tooling relies on `PATH`/`dump_bin` (above).
- QA previews pointing at a shared `PGDATA` drop a `PREVIEW_OWNER` marker
  file naming the owning instance/branch — check it before wiping (T365
  preview incident).

[wiki:storage-adapters]: http://localhost:2137/wiki/storage-adapters
[fergusstrange/embedded-postgres]: https://github.com/fergusstrange/embedded-postgres
