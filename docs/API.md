# Orenda — REST API Reference

Base URL: `http://127.0.0.1:2137`

Authentication:
- **Cookie** (`orenda_session`, HttpOnly, SameSite=Lax) — set by `POST /api/v1/auth/login`. Used by the SPA.
- **Bearer API token** — `Authorization: Bearer <token>`. Used by agents under `/api/v1/agent/*`.

All JSON. Errors are `{"error": "<code>"}` with a 4xx/5xx status.

## Auth

| Method | Path | Auth | Notes |
|---|---|---|---|
| POST | `/api/v1/auth/login` | — | `{email, password}` → sets cookie + returns `{token}` |
| POST | `/api/v1/auth/logout` | cookie | clears cookie |
| GET  | `/api/v1/me` | cookie | current profile + scopes |

## Meta

| Method | Path | Notes |
|---|---|---|
| GET | `/healthz` | `{status, version}` |
| GET | `/api/v1/info` | `{version, name, capabilities}` |
| GET | `/api/v1/openapi.yaml` | OpenAPI 3.1 spec (Phase 24). Public, no auth — the spec isn't secret. Embedded at compile time. Canonical copy: `docs/openapi.yaml`; the binary serves the synced copy `internal/api/openapi.yaml`. Drift gate `TestOpenAPI_EmbeddedCopyMatchesDocs` fails when they diverge — resync with `make openapi-sync`. |
| GET | `/api/v1/stats` | uptime + request counters (2xx/3xx/4xx/5xx) + slow-request count + ws subscribers + db file size (Phase 24). Public, no auth |
| GET | `/api/v1/ws?token=<jwt>` | WebSocket upgrade |
| POST | `/api/v1/events/await` | long-poll `{topic, timeout_s}` |

## Projects / Kanban

| Method | Path | Notes |
|---|---|---|
| GET/POST | `/api/v1/projects` | list / create (auto-creates board + 5 columns) |
| GET/PATCH/DELETE | `/api/v1/projects/{id}` | |
| GET | `/api/v1/projects/{id}/board` | board + columns; tasks carry `counters` + `blocked_by_count` (Phase 17) |
| GET/POST | `/api/v1/projects/{id}/tasks` | filter: `?status=`, `?column_id=`. Listing endpoints populate `counters` + `blocked_by_count` (Phase 17) |
| GET | `/api/v1/projects/{id}/activity` | project-wide activity feed |
| GET/POST | `/api/v1/projects/{id}/attachments` | project-level attachments |
| POST | `/api/v1/projects/{id}/columns` | add column `{name, color?, wip_limit?}` (position assigned) |
| PATCH | `/api/v1/columns/{id}` | name, position, wip_limit, color |
| DELETE | `/api/v1/columns/{id}` | 422 while tasks remain (`current` count in body) |

## Tasks

| Method | Path | Notes |
|---|---|---|
| GET | `/api/v1/tasks/{id}` | |
| PATCH/PUT | `/api/v1/tasks/{id}` | partial update (PUT = alias). Mutable: title, description, status, priority, assignee_type/assignee_id, project_id, column_id, parent_task_id, context_md, agent_notes, color, tags, due_at, time_estimate_s, position… Status/column moves follow the 27.8 single-axis rule: PATCHing one syncs the other; `status=done` stamps `completed_at` |
| DELETE | `/api/v1/tasks/{id}` | |
| POST | `/api/v1/tasks/{id}/move` | `{column_id, position?}` |
| POST | `/api/v1/tasks/{id}/claim` | `{agent_id}` — 409 on lock_taken |
| POST | `/api/v1/tasks/{id}/release` | |
| POST | `/api/v1/tasks/{id}/submit` | `{agent_id, note?}` → status=review, awaiting=human |
| POST | `/api/v1/tasks/{id}/review` | `{decision: approve\|reject, comment?}` |
| GET | `/api/v1/tasks/{id}/children` | list direct child tasks + `{progress: {total, done}}` (Phase 14) |
| GET | `/api/v1/tasks/{id}/blockers` | all blockers (open + satisfied), Phase 15 |
| GET | `/api/v1/tasks/{id}/dependents` | reverse lookup, Phase 15 |
| PUT | `/api/v1/tasks/{id}/dependencies` | `{depends_on_ids: [...]}` — replace; cycles/self-loops → 422 |
| GET/POST | `/api/v1/tasks/{id}/comments` | body: `{body_md}` — `@user:<id>`/`@agent:<id>` mentions |
| GET/POST | `/api/v1/tasks/{id}/attachments` | list / multipart upload (`file` field) |
| GET | `/api/v1/tasks/{id}/attachments/{attId}/download` | stream file |
| GET/PUT | `/api/v1/tasks/{id}/tags` | list / replace tag set `{tag_ids: [...]}` (empty array clears) |
| GET/POST | `/api/v1/tasks/{id}/checklists` | list (with items) / create `{title}` |
| DELETE | `/api/v1/tasks/{id}/checklists/{clId}` | cascades items |
| GET/POST | `/api/v1/tasks/{id}/checklists/{clId}/items` | |
| PATCH/DELETE | `/api/v1/tasks/{id}/checklists/{clId}/items/{itemId}` | `{done?, title?}` / delete |
| GET | `/api/v1/tasks/{id}/activity` | audit log |
| GET | `/api/v1/tasks/{id}/context` | task + comments + activity + children + checklists |
| GET | `/api/v1/attachments/{attId}/download` | global download alias |

### Tags (Phase 13)

Global catalogue (not project-scoped); assignment is per-task via `PUT /tasks/{id}/tags`.

| Method | Path | Notes |
|---|---|---|
| GET/POST | `/api/v1/tags` | list / create `{name, color?}` |
| PATCH/DELETE | `/api/v1/tags/{id}` | `{name?, color?}` — empty color clears |

### Child tasks (Phase 14)

Subtasks were promoted to first-class tasks in Phase 14. To create a child, POST `/api/v1/projects/{projectID}/tasks` with `parent_task_id` set:

```
POST /api/v1/projects/{pid}/tasks
{ "title": "first", "parent_task_id": "<parent-id>" }
```

Listing, status changes, and deletion all use the standard task endpoints. `GET /api/v1/tasks/{id}/children` returns `{tasks: [...], progress: {total, done}}` for the parent task's progress bar.

Subtask-related endpoints (`/api/v1/tasks/{id}/subtasks[/{subId}]`) and the legacy `task.subtask_added` activity events are gone.

### Review queue (Phase 19)

Tasks awaiting human action — the union of `awaiting='human'` and `status='review'`. Newest first. Includes inbox tasks; `project_name`/`project_color` are empty strings for those.

| Method | Path | Notes |
|---|---|---|
| GET | `/api/v1/review-queue` | `{tasks: [ReviewQueueItem], count}` |
| GET | `/api/v1/review-queue/count` | `{count}` — cheap, used by the sidebar badge |
| GET | `/api/v1/today` | `{overdue, due_today, scheduled_today, awaiting_count}` (Phase 20). One round-trip for the daily-driver dashboard |
| GET/POST | `/api/v1/courses` | Phase 18 LMS. `POST` body: `{title, intent_md, skip_generator?}` (skip_generator — Phase 27.6, no tutor task) |
| GET/DELETE | `/api/v1/courses/{id}` | GET returns the full tree (course + modules + lessons + quizzes + progress); DELETE cascades |
| POST | `/api/v1/courses/{id}/approve` | review → active (unlocks the first lesson) |
| POST | `/api/v1/courses/{id}/request-changes` | review → draft |
| POST | `/api/v1/lessons/{id}/complete` | lesson done → next unlocks; last one closes the course |
| POST | `/api/v1/lessons/{id}/quizzes/{qid}/answer` | exact → instant verdict; open → review task for the tutor |
| PUT | `/api/v1/courses/{id}/curriculum` | Phase 27.6 owner-side atomic swap: `{modules: [{title, description?, position, lessons: [{title, position, content_md?, quizzes?: [{position, question_md, expected_md?, kind}]}]}]}`. Same service path as the tutor agent — when called from the owner namespace, the service retires the generator task so a sleeping tutor can't overwrite manual work. |
| POST | `/api/v1/lessons/{id}/quizzes` | Phase 27.6 owner-side quiz append (closes Phase 18.6 debt). Body: `{position?: 0=append, question_md, expected_md?, kind: 'exact'|'open'}`. Returns the persisted quiz (with id + assigned position). |
| PUT | `/api/v1/lessons/{id}/content` | Phase 27.6 owner-side content edit (active courses only by design). Body: `{content_md, task_id?}`. Doesn't flip lesson status — used for typos / rewordings once the course is live. |
| POST | `/api/v1/courses/{id}/modules` | Phase 30.13 granular structure edits (stable IDs → student progress survives). Body `{title, description?}` → 201; appended at end. Allowed on draft/review/active; done/archived → 422 `invalid_transition`. Same error mapping for all rows below: 400 `invalid_input`, 404 `not_found`. |
| PUT | `/api/v1/courses/{id}/structure` | Phase 30.13 drag&drop reorder. Body `{modules: [{module_id, lesson_ids: []}]}` — IDs only, must name every module and lesson of the course exactly once (else 400, nothing written); lessons may move across modules; positions rewritten 1..n. Returns the refreshed course tree (same shape as `GET /courses/{id}`). |
| PATCH/DELETE | `/api/v1/modules/{id}` | Phase 30.13: rename/description in place; DELETE cascades lessons+quizzes (that progress is dropped by the owner's explicit choice). |
| POST | `/api/v1/modules/{id}/lessons` | Phase 30.13: append a lesson; it is born `locked` even in an active course (materialize before students see it). |
| PATCH/DELETE | `/api/v1/lessons/{id}` | Phase 30.13: rename in place (status/content/task link preserved); DELETE cascades quizzes. |
| PATCH/DELETE | `/api/v1/quizzes/{qid}` | Phase 30.13: edit `{question_md, expected_md?, kind?}` in place (empty kind = `exact`); DELETE removes the quiz. |

Inline accept / return goes through the standard review endpoint (`POST /api/v1/tasks/{id}/review` with `decision: approve|reject`). Approval moves the task to `done`; return moves it back to `in_progress` + `awaiting='agent'` and (optionally) records a comment the agent sees on resume.

## Agents (admin, cookie auth)

| Method | Path | Notes |
|---|---|---|
| GET/POST | `/api/v1/agents` | list / create (returns `plain_token` once) |
| GET/DELETE | `/api/v1/agents/{id}` | |
| POST | `/api/v1/agents/{id}/heartbeat` | |

## Agents (self, Bearer token)

| Method | Path | Notes |
|---|---|---|
| GET | `/api/v1/agent/me` | the bound agent |
| POST | `/api/v1/agent/heartbeat` | marks online |
| GET | `/api/v1/agent/projects` | list all projects (single-owner) — the source of `project_id` for task proposal; name feeds branch naming. Same shape as the user-side list. |
| GET | `/api/v1/agent/tasks?ready=true&limit=N` | list work surface (Phase 15); `ready` filters out blocked + claimed |
| POST | `/api/v1/agent/tasks` | propose a NEW task (Phase 33.1): `{project_id, title, description_md, priority?, blocked_by?, parent_task_id?}` → 201, lands `backlog` + `awaiting=human` (review-queue triage); 400 missing fields, 404 unknown project/parent/blocker |
| POST | `/api/v1/agent/tasks/{id}/claim` | atomic claim; 409 `lock_taken`; 422 `task_blocked` + `unfinished_blockers` (Phase 15) |
| POST | `/api/v1/agent/tasks/{id}/release` | |
| POST | `/api/v1/agent/tasks/{id}/submit` | Task 87 gate: 422 `time_not_logged` while the task has zero spent time and the bearer agent has no open timer on it; passes with a running auto-timer or after any manual entry |
| POST | `/api/v1/agent/tasks/{id}/time` | Task 87: manual entry `{minutes >= 0}` → 201, closed `source=manual` row + `time_spent_s` accrual; 0 minutes = time-tracked-trivial bypass |
| GET | `/api/v1/agent/tasks/{id}/context` | 403 if not assigned |
| GET | `/api/v1/agent/tasks/{id}/checklists` | T96: list the task's checklists with their items (`{checklists, checklist_items}` — items keyed by checklist id). Open read: claimable or held. |
| POST | `/api/v1/agent/tasks/{id}/checklists` | T96: create a checklist `{title}` → 201. Lock holder only; 403 `not_lock_holder` otherwise. |
| POST | `/api/v1/agent/tasks/{id}/checklists/{clId}/items` | T96: append an item `{title}` → 201. Holder only; a `clId` outside the path task → 404. |
| PATCH | `/api/v1/agent/tasks/{id}/checklists/{clId}/items/{itemId}` | T96: partial update `{done?, title?}` → 204. Holder only. Ticking done emits `task.checklist_item_done` with the agent as actor. |
| DELETE | `/api/v1/agent/tasks/{id}/checklists/{clId}/items/{itemId}` | T96: delete an item → 204. Holder only. |
| GET | `/api/v1/agent/courses?status=draft` | Phase 18: list courses the tutor can claim |
| PUT | `/api/v1/agent/courses/{id}/curriculum` | body `{modules: [{title, description?, position, lessons: [{title, position, content_md?, quizzes?: [{position, question_md, expected_md?, kind}]}]}]}` — atomic swap (Phase 27.6 added per-lesson `quizzes` and per-module `description`) |
| POST | `/api/v1/agent/lessons/{id}/materialize` | Phase 27.4: tutor writes content + unlocks the lesson |
| PUT | `/api/v1/agent/lessons/{id}/content` | Phase 27.4: in-place content update |
| POST | `/api/v1/agent/lessons/{id}/quizzes` | Phase 27.6: append a single quiz (closes Phase 18.6 debt). Same body as the user-side endpoint. |
| POST | `/api/v1/agent/courses/{id}/modules` | Phase 30.13: agent mirrors of the granular structure ops — same handlers, same progress-preserving semantics and error mapping (400 `invalid_input`, 404 `not_found`, 422 `invalid_transition`). |
| PUT | `/api/v1/agent/courses/{id}/structure` | Phase 30.13: IDs-only reorder, exact coverage; returns the refreshed tree. |
| PATCH/DELETE | `/api/v1/agent/modules/{id}` · POST `/api/v1/agent/modules/{id}/lessons` | Phase 30.13 agent-side. |
| PATCH/DELETE | `/api/v1/agent/lessons/{id}` | Phase 30.13: rename / delete (cascade). |
| PATCH/DELETE | `/api/v1/agent/quizzes/{qid}` | Phase 30.13: edit / delete quiz. |

The `orenda agent` CLI (Phase 25) is a thin cobra wrapper over the
agent namespace. Source: `cmd/orenda/agent.go`. See
`docs/skills/orenda/SKILL.md` for the etiquette + workflow.

## Calendar / Time

| Method | Path | Notes |
|---|---|---|
| GET | `/api/v1/events?from=&to=&project_id=` | RFC3339 range |
| POST | `/api/v1/events` | create; supports `recurrence` RRULE (DAILY/WEEKLY/MONTHLY, INTERVAL, COUNT, UNTIL) |
| GET/PATCH/DELETE | `/api/v1/events/{id}` | |
| POST | `/api/v1/tasks/{id}/timer/start` | one open timer per actor |
| POST | `/api/v1/tasks/{id}/timer/stop` | |
| POST | `/api/v1/tasks/{id}/time` | `{agent_id?, start_at, end_at}` manual entry |
| GET | `/api/v1/reports/time` | `?agent_id=&from=&to=` per-task aggregation |

## Wiki / Search

| Method | Path | Notes |
|---|---|---|
| GET | `/api/v1/pages` | tree |
| POST | `/api/v1/pages` | create (auto-parses `[[slug]]`) |
| GET/PUT | `/api/v1/pages/{slug}` | |
| DELETE | `/api/v1/pages/{slug}` | |
| PATCH | `/api/v1/pages/{slug}/move` | re-parent the page |
| GET | `/api/v1/pages/{slug}/backlinks` | |
| GET | `/api/v1/search?q=&type=&limit=` | FTS5 BM25 over pages/tasks/comments |

## Notifications

| Method | Path | Notes |
|---|---|---|
| GET | `/api/v1/notifications?limit=` | unread first; `{unread: n}` |
| POST | `/api/v1/notifications/{id}/read` | |
| GET/POST | `/api/v1/notifications/subscriptions` | bot subscriptions list / create |
| DELETE | `/api/v1/notifications/subscriptions/{id}` | |
| POST | `/api/v1/bots/telegram/bind` | Phase 22.3: resolve one-shot `/start` code → chat_id → subscription |
| POST | `/api/v1/webhooks/vk` | VK callback (confirmation token, replay-protected) — unauthenticated by design |

## Backups

| Method | Path | Notes |
|---|---|---|
| GET | `/api/v1/backups/settings` | `{enabled, remote_url, has_auth, source_hint?}`. `source_hint` is `"ui_override_restart_to_apply"` when a UI override diverges from the in-memory cfg (the running `*backup.Service` is still on the old URL — restart to apply). |
| PUT | `/api/v1/backups/settings` | body `{enabled?, remote_url?, remote_auth?}`; persists to the `backup_settings` table; returns the merged `BackupSettings` (200). Validation: `enabled=true` requires a non-empty `remote_url`; allowed URL schemes are `http`, `https`, `ssh`, `git`. The running `*backup.Service` is wired from cfg at startup and stays on the old URL until the operator restarts — see SESSION.md «Фаза «Полировка»» for the restart-to-apply contract. |
| POST | `/api/v1/backups/test` | git push of mirror |
| POST | `/api/v1/backups/snapshot` | write snapshot now |
| GET | `/api/v1/backups/snapshots` | list |
| POST | `/api/v1/backups/restore` | body `{path, force?}`; refuses when the server is up and returns a hint pointing at the CLI (`orenda backup restore --from <path> --yes`). The CLI command (Phase 22) writes a safety-copy to `<dest>.pre-restore-<ts>`, runs migrations, and verifies with `integrity_check` + `foreign_key_check`. With `force=true` and maintenance mode on (Phase 22.3), restores in-process: drains WS, swaps the file, re-runs migrations, verifies, keeps maintenance on for the operator to verify. |
| GET/POST | `/api/v1/maintenance[/on\|off]` | Phase 22.3: maintenance mode. Reads always pass; writes 503 while on. Combine with `POST /backups/restore {force: true}` to do an in-process restore with WS drain. |
| GET | `/api/v1/backups/log` | recent log |

## Offline sync

| Method | Path | Notes |
|---|---|---|
| POST | `/api/v1/sync` | `{ops: [{op, target, payload, client_id, created_at}]}` — idempotent via `sync_ops` |

## Conventions

- IDs are UUIDv7 strings.
- Timestamps are RFC3339 (output) / both RFC3339 and `YYYY-MM-DD HH:MM:SS` accepted on input.
- Rate limits: 60 burst / 20 rps anonymous per IP, 300/100 authenticated per identity. `429` carries `Retry-After`.
- CORS: loopback origins only.
