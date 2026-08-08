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
| GET | `/api/v1/ws?token=<jwt>` | WebSocket upgrade |
| POST | `/api/v1/events/await` | long-poll `{topic, timeout_s}` |

## Projects / Kanban

| Method | Path | Notes |
|---|---|---|
| GET/POST | `/api/v1/projects` | list / create (auto-creates board + 5 columns) |
| GET/PATCH/DELETE | `/api/v1/projects/{id}` | |
| GET | `/api/v1/projects/{id}/board` | board + columns |
| GET/POST | `/api/v1/projects/{id}/tasks` | filter: `?status=`, `?column_id=` |
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
| GET/POST | `/api/v1/tasks/{id}/subtasks` | |
| GET/POST | `/api/v1/tasks/{id}/comments` | body: `{body_md}` — `@user:<id>`/`@agent:<id>` mentions |
| POST | `/api/v1/tasks/{id}/attachments` | multipart `file` field |
| GET | `/api/v1/tasks/{id}/activity` | audit log |
| GET | `/api/v1/tasks/{id}/context` | task + comments + activity + subtasks |

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
| POST | `/api/v1/agent/tasks/{id}/claim` | agent_id from token |
| POST | `/api/v1/agent/tasks/{id}/release` | |
| POST | `/api/v1/agent/tasks/{id}/submit` | |
| GET | `/api/v1/agent/tasks/{id}/context` | 403 if not assigned |

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
