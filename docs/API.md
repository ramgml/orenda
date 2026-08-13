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
| GET | `/api/v1/openapi.yaml` | OpenAPI 3.1 spec (Phase 24). Public, no auth — the spec isn't secret. Embedded at compile time. |
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
| PATCH | `/api/v1/columns/{id}` | name, position, wip_limit |

## Tasks

| Method | Path | Notes |
|---|---|---|
| GET | `/api/v1/tasks/{id}` | |
| PATCH/PUT | `/api/v1/tasks/{id}` | partial update |
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
| POST | `/api/v1/tasks/{id}/attachments` | multipart `file` field |
| GET | `/api/v1/tasks/{id}/activity` | audit log |
| GET | `/api/v1/tasks/{id}/context` | task + comments + activity + children + checklists |

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
| GET/POST | `/api/v1/courses[/{id}]` | Phase 18 LMS. `POST` body: `{title, intent_md, skip_generator?}` (Phase 27.6). `GET /{id}` returns the full tree (course + modules + lessons + quizzes + progress) |
| PUT | `/api/v1/courses/{id}/curriculum` | Phase 27.6 owner-side atomic swap: `{modules: [{title, description?, position, lessons: [{title, position, content_md?, quizzes?: [{position, question_md, expected_md?, kind}]}]}]}`. Same service path as the tutor agent — when called from the owner namespace, the service retires the generator task so a sleeping tutor can't overwrite manual work. |
| POST | `/api/v1/lessons/{id}/quizzes` | Phase 27.6 owner-side quiz append (closes Phase 18.6 debt). Body: `{position?: 0=append, question_md, expected_md?, kind: 'exact'|'open'}`. Returns the persisted quiz (with id + assigned position). |
| PUT | `/api/v1/lessons/{id}/content` | Phase 27.6 owner-side content edit (active courses only by design). Body: `{content_md, task_id?}`. Doesn't flip lesson status — used for typos / rewordings once the course is live. |

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
| GET | `/api/v1/agent/tasks?ready=true&limit=N` | list work surface (Phase 15); `ready` filters out blocked + claimed |
| POST | `/api/v1/agent/tasks/{id}/claim` | atomic claim; 409 `lock_taken`; 422 `task_blocked` + `unfinished_blockers` (Phase 15) |
| POST | `/api/v1/agent/tasks/{id}/release` | |
| POST | `/api/v1/agent/tasks/{id}/submit` | |
| GET | `/api/v1/agent/tasks/{id}/context` | 403 if not assigned |
| GET | `/api/v1/agent/courses?status=draft` | Phase 18: list courses the tutor can claim |
| PUT | `/api/v1/agent/courses/{id}/curriculum` | body `{modules: [{title, description?, position, lessons: [{title, position, content_md?, quizzes?: [{position, question_md, expected_md?, kind}]}]}]}` — atomic swap (Phase 27.6 added per-lesson `quizzes` and per-module `description`) |
| POST | `/api/v1/agent/lessons/{id}/materialize` | Phase 27.4: tutor writes content + unlocks the lesson |
| PUT | `/api/v1/agent/lessons/{id}/content` | Phase 27.4: in-place content update |
| POST | `/api/v1/agent/lessons/{id}/quizzes` | Phase 27.6: append a single quiz (closes Phase 18.6 debt). Same body as the user-side endpoint. |

The `orenda agent` CLI (Phase 25) is a thin cobra wrapper over the
agent namespace. Source: `cmd/orenda/agent.go`. See
`docs/skills/orenda/SKILL.md` for the etiquette + workflow.

## Calendar / Time

| Method | Path | Notes |
|---|---|---|
| GET | `/api/v1/events?from=&to=&project_id=` | RFC3339 range |
| POST/PATCH/DELETE | `/api/v1/events[/{id}]` | supports `recurrence` RRULE (DAILY/WEEKLY/MONTHLY, INTERVAL, COUNT, UNTIL) |
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
| GET | `/api/v1/pages/{slug}/backlinks` | |
| GET | `/api/v1/search?q=&type=&limit=` | FTS5 BM25 over pages/tasks/comments |

## Notifications

| Method | Path | Notes |
|---|---|---|
| GET | `/api/v1/notifications?limit=` | unread first; `{unread: n}` |
| POST | `/api/v1/notifications/{id}/read` | |

## Backups

| Method | Path | Notes |
|---|---|---|
| GET | `/api/v1/backups/settings` | `{enabled, remote_url, has_auth}` |
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
