---
module: github.com/ramgml/orenda
status: pre-alpha
audience: contributors + AI agents extending the system
---

# Orenda — Architecture

> Companion to [[PRD]] and [[PLAN]]: what the code looks like and why.
> Updated each phase that touches the architecture.

## 0. One-sentence summary

Orenda is a single Go binary that embeds a React SPA, talks to a
local SQLite database, and exposes a REST API plus WebSocket hub for
external AI-agents. Everything that needs a separate process lives
inside the binary: backup scheduler, bot registry, mirror writer,
Markdown FTS5 search, MCP server.

```
┌───────────────────────────────────────────────────────────────────┐
│                    orenda (single binary, port 2137)              │
│                                                                   │
│   ┌─────────────┐   ┌──────────────┐   ┌─────────────────────┐   │
│   │ HTTP / chi  │   │  WS hub      │   │  MCP server          │   │
│   │ handlers    │◄──┤  (gorilla)   │   │  (Phase 25, stdio    │   │
│   │             │   │  8 topics    │   │   or /api/v1/agent/  │   │
│   └──────┬──────┘   └──────────────┘   │   mcp)              │   │
│          │                             └──────────┬──────────┘   │
│          ▼                                        ▼              │
│   ┌─────────────────────────────────────────────────────────┐    │
│   │                internal/service/*  (business rules)      │    │
│   │  task · agent · comment · attachment · activity · event  │    │
│   │  timeentry · wiki · search · notifier · course           │    │
│   └──────┬─────────────────────────────────────┬────────────┘    │
│          ▼                                     ▼                 │
│   ┌────────────────┐                  ┌──────────────────────┐   │
│   │internal/storage│                  │ internal/domain/*    │   │
│   │ sqlite         │◄──── contracts ──┤ interfaces + DTOs    │   │
│   │ (migrations)   │                  └──────────────────────┘   │
│   └────────┬───────┘                                            │
│            ▼                                                    │
│        SQLite WAL  (data/orenda.db)                             │
└───────────────────────────────────────────────────────────────────┘
            ▲                                       ▲
            │ REST + WS                             │ bundled assets
            │                                       │ (//go:embed all:dist)
   ┌────────┴───────┐                       ┌────────┴─────────┐
   │  External AI   │                       │   React SPA      │
   │  agents        │                       │   web/dist/      │
   │ (Bearer token) │                       │   (Vite + RTKQ)  │
   └────────────────┘                       └──────────────────┘
```

## 1. Process model

A single `orenda` binary serves **all** of the following in one OS
process. Sub-processes / external dependencies are listed in §1.2.

### 1.1. Goroutines inside the binary

| Concern | Goroutine | Source |
|---|---|---|
| HTTP server | one per `Listener` | `cmd/orenda/main.go::serveHTTP` |
| pprof debugger listener (opt-in) | one per process | `cmd/orenda/main.go` gated on `server.debug_pprof` |
| Backup scheduler | one tick loop (5m / 15m / 24h) | `internal/backup/scheduler.go` |
| Mirror scheduler | one tick loop (5m) | `internal/mirror/scheduler.go` |
| WebSocket hub | one fan-out goroutine + per-client pumps | `internal/api/ws/hub.go` |
| MCP server | tied to HTTP request lifecycle (Streamable HTTP) | `internal/mcp/server.go` |
| Long-poll `/events/await` | per-request blocking on `sync.Cond` | `internal/api/handlers_phase3.go` |
| Telegram bot long-poll | one goroutine per bot | `internal/bot/telegram.go` |
| zsh backward-compat (?) — n/a | — | — |

The Hub is the only place with a "many goroutines writing to a
single channel" pattern; it's bounded by the per-user filter and
the drop-on-full policy documented in §5.

### 1.2. External dependencies

| Tool | Why | When installed |
|---|---|---|
| `git` CLI (via `os/exec`) | push to backup remote | required by Phase 7 |
| `govulncheck` (Go tool) | security scan of `go.mod` graph | only for `make govulncheck` (Phase 28.6) |
| `systemd --user` | install target | optional, only `scripts/install.sh --systemd` |

No CGo. SQLite is pure-Go (`modernc.org/sqlite`). The binary is
fully static.

## 2. Directory map and module ownership

```
orenda/
├── cmd/orenda/main.go              # Entry point + cobra CLI
├── internal/
│   ├── api/                         # HTTP + WS + middleware
│   ├── auth/                        # JWT (cookie) + opaque API tokens
│   ├── backup/                      # git push + sqlite .backup
│   ├── bot/                         # pluggable bots (Phase 10)
│   ├── config/                      # yaml + env (spf13/viper-style)
│   ├── domain/                      # entities + repository INTERFACES
│   ├── embed/web/                   # //go:embed all:dist + placeholder
│   ├── mcp/                         # MCP stdio+HTTP server (Phase 25)
│   ├── mirror/                      # markdown mirror writer
│   ├── service/                     # business logic
│   └── storage/sqlite/              # repos + migrations (the only DB driver)
├── web/                             # React 18 + TS + Vite + Tailwind
├── data/                            # runtime (gitignored)
├── docs/
│   ├── PRD.md · PLAN.md · ARCHITECTURE.md (this) · CONTEXT.md
│   ├── API.md · DB.md · openapi.yaml
│   ├── SESSION.md · CHANGELOG.md
│   └── skills/orenda/SKILL.md
├── scripts/                         # install.sh, systemd unit, uninstall.sh
├── Makefile                         # build / test / lint / e2e
└── go.mod
```

The contract: anything in `internal/` can read freely, anything under
`web/` cannot. Cross-package boundaries go through interfaces in
`internal/domain/*` — code under `internal/service/*` references the
domain interfaces, not `internal/storage/sqlite/*` directly. This is
how the test suite swaps repos for in-memory stand-ins (see §7.2).

## 3. Layered architecture

The dependency graph is strictly downwards:

```
                            cmd/orenda (composition root)
                                       │
   ┌───────────────────────────────────┼───────────────────────────────┐
   ▼                                   ▼                               ▼
internal/api                  internal/bot                  internal/backup
  HTTP+WS                       bot interface                backup scheduler
   │                                   │                          │
   │ uses                              │ emits events             │ reads cfg via
   ▼                                   │                          │ atomic.Pointer
internal/service                    ▼                          (Phase 28.9)
  business rules      internal/mirror / notifier etc.
   │
   ▼
internal/domain
  contracts
   △
   │ implements
internal/storage/sqlite
  migrations + repos
```

What this means in practice:

- A change to a `service.Task` interface signature forces every
  storage repo to update. The reverse is not true — adding a new
  method on the repo does not propagate to services unless something
  calls it.
- HTTP handlers live in `internal/api/handlers_*.go`. Each handler
  has access to a `Dependencies` struct (composition of ports) so
  tests can swap individual ports in isolation.
- The bot registry (`internal/bot/registry.go`) is wired into the
  Notifier service — bots don't reach into the domain directly.

## 4. Data flow — three reference scenarios

### 4.1. Owner creates a task from the kanban board

```
ui (BoardColumn dragend)
     │
     ▼   POST /api/v1/tasks {title, project_id, column_id, ...}
router ─► RequireUser middleware (JWT cookie)
     │
     ▼
createTaskHandler
     │  validate → store.CreateTask(repo.TaskCreate{...})
     │                              │
     ▼                              ▼
notifier.Notify(TaskCreated)    repo.CreateTask
     │                              │
     │                              ▼
     │                          sqlite INSERT
     ▼                              │
ws.Hub.Publish("tasks", TaskCreated)│
     │                              ▼
     ▼                          cmd/orenda/main.go::recordActivity
ui (second tab)                        (activity row)
  wsClient.on("tasks", handler)
     │
     ▼
react-query refetch /projects/:id/board
```

Latency target: <50ms p95 for the create + WS fan-out. The bottleneck
in dev is usually the `notifier.Notify` → SQLite → Hub publish
critical section.

### 4.2. AI-agent claims and submits a task

```
agent (curl / orenda agent claim)
     │
     ▼   POST /api/v1/agent/tasks/:id/claim  (Authorization: Bearer ...)
router ─► RequireAgent middleware (opaque token lookup)
     │
     ▼
claimHandler
     │  verify token + scopes['tasks.claim']
     │  lock table → UPDATE tasks SET assignee_type='agent', ...
     ▼
task.Service.Claim
     │  early-return 409 lock_taken with holder fields
     │  early-return 422 blocked (Phase 15 dependency graph)
     │  on success: activity row + WS publish task.claimed
     ▼
sqlite UPDATE (atomic) + INSERT into task_locks
     │
     ▼
ws.Hub.Publish("tasks", task.claimed)
   (filtered to the owner's user_id)
```

The agent never holds a persistent connection — every call is a
sync request. Long-poll `/api/v1/events/await` is the optional
push-channel for agents that don't want to manage WS state (used
by `orenda agent await`, see §6).

### 4.3. PWA offline write — sync flush

```
ui /api/v1/tasks POST  ─► service-worker intercepts
  if !navigator.onLine:
       write to IndexedDB outbox (with client_id)
  else:
       forward to REST as normal
       (no outbox)
       ▼
ui periodic sync ping
     │
     ▼
POST /api/v1/sync {ops:[{op, target, payload, client_id, ts}]}
     │
     ▼
syncHandler
   per op:
     ├─ check sync_ops table (idempotency)
     ├─ execute (or surface conflict error per op)
     └─ emit per-op result
     ▼
ui react-query cache selective invalidation
```

Conflict policy: last-write-wins by `updated_at` (documented in
Phase 8 §6). The `sync_ops` table tracks who wrote what; clients
send `client_id` for dedup.

## 5. Authentication — two parallel models

| Surface | What | Where |
|---|---|---|
| **UI (browser)** | JWT in `orenda_session` httpOnly cookie | `internal/auth/jwt.go` + `internal/api/handlers_auth.go` |
| **AI agents** | Opaque API token (32-byte base64url) hashed with bcrypt in `api_tokens` | `internal/auth/apitoken.go` |
| **Webhook receivers** | HMAC-SHA256 of payload + timestamp | `internal/bot/webhook.go` |

The split is deliberate:

- The browser cannot keep a secret long-term → JWT cookie with a
  lifetime that's capped by `cfg.auth.jwt_ttl` (24h default after
  Phase 28.4).
- Agents run unattended and DO keep the secret → opaque token with
  rotation support (no JWT expiry juggling).
- Two namespaces in the router (`RequireUser` vs `RequireAgent`)
  prevent accidental cross-auth: a JWT cookie on `/api/v1/agent/*`
  gets a 401 (Phase 27.11 found this very class of bug).

JWT TTL and cookie `Secure` both read from config so dev (loopback,
HTTP) and prod (reverse proxy, HTTPS) agree on defaults. Phase 28.4
set the safe defaults and left overrides for operators who want them.

## 6. Real-time — WebSocket hub

`internal/api/ws/hub.go` is the single channel of real-time push.
It is per-user (each `*Client` has its own outbound goroutine) and
per-topic (subscribers pick from the eight topics listed below). The
hub is non-blocking: if a subscriber's send buffer is full the
message is dropped and logged. Dropping is preferred over buffering
because the message becomes actionable one round-trip later as a
fresh REST/WS read.

### 6.1. Topics

```go
const (
    TopicTasks         = "tasks"          // task.* + project.* + board.* + column.*
    TopicAgents        = "agents"         // agent.* (heartbeat, claim, release)
    TopicAttachments   = "attachments"    // upload + dedup
    TopicComments      = "comments"       // comment.created, mention.created
    TopicEvents        = "events"         // calendar events
    TopicNotifications = "notifications"  // bell UI
    TopicTimers        = "timers"         // time_entries.started, .stopped
    TopicWiki          = "wiki"           // page saved / backlink recalc
)
```

A single owner connection subscribes to all eight topics via the
`subscribeAll(hub, userID)` helper (Phase 27.9 closed the latent
gap where UI only got `tasks` traffic). Per-project filtering is
not implemented — single-owner means one user_id = the subscription
root.

### 6.2. Auth handshake

`GET /api/v1/ws` upgrade handler accepts the token via three
precedences:

1. `orenda_session` cookie (the browser path; Phase 27.2).
2. `Authorization: Bearer <jwt>` (devtools / curl).
3. `?token=<jwt>` query param (CLI; deprecated but kept for
   back-compat).

Without any of the three → 401. The session therefore lives in
`AuthContext` only as a flag (`status === 'authenticated'`), never
as the literal token (Phase 27.2 found the literal `token` field
was always null anyway).

### 6.3. Long-poll fallback

For agents that don't speak WS, `POST /api/v1/events/await` is a
long-poll that:

- takes `{timeout_s, since, filter}`, defaults 30s,
- subscribes its connection to the hub under a synthetic per-call id,
- waits on `sync.Cond` for any event matching the filter,
- returns the batch or `204 No Content` when the timeout fires.

Implemented via the agent-side `cmd/orenda/agent.go::await`. The
hub itself doesn't know about HTTP — the handler does the bridge.

## 7. Persistence

### 7.1. SQLite configuration

- Pure-Go driver: `modernc.org/sqlite`. The `data/orenda.db` file is
  single-instance.
- WAL mode (`PRAGMA journal_mode = WAL`) — required for parallel
  reads while a write is in flight.
- `PRAGMA busy_timeout = 5000` — five seconds before returning
  `SQLITE_BUSY`.
- `PRAGMA foreign_keys = ON` — set ON at every connection open.
  The migration runner can flip it OFF around destructive
  migrations (Phase 16 §1) via the `-- orenda:foreign_keys_off`
  marker.

### 7.2. Migration runner

`internal/storage/sqlite/db.go` runs migrations from
`internal/storage/sqlite/migrations/` in lexicographic order. Every
applied migration is recorded in `schema_migrations(version, applied_at)`.
Each version has a paired `.down.sql` (Phase 4 / Wave 4).

The down-runner refuses to rollback migrations marked
`-- orenda:irreversible` (e.g. 001_init, 013_subtasks_to_children,
015_inbox_no_project).

### 7.3. ID generation

UUIDv7 strings for primary keys throughout (`tasks.id`, `projects.id`,
etc.). Generation is `uuid.NewV7()` from `github.com/google/uuid` —
the timestamp prefix gives chronological sort order without forcing
an additional index on `created_at` in every list query.

### 7.4. FTS5

Three virtual tables: `tasks_fts`, `pages_fts`, `comments_fts`.
Backed by `content_rowid='rowid'` so an `INSERT INTO … ('rebuild')`
syncs after table-rebuild migrations. Search results return both
the IDs and the highlighted snippet; ranking uses BM25.

## 8. Frontend layout

The `web/` tree follows a feature-based convention:

```
web/src/
├── main.tsx                  # Vite entrypoint
├── App.tsx                   # router + RequireAuth
├── features/                 # business features
│   ├── auth/                # LoginPage, AuthContext
│   ├── kanban/              # Board, BoardColumn, TaskCard, EditColumnModal
│   ├── tasks/               # TaskModal, TaskViewBody, ChildTasksList, TaskFieldControls
│   ├── inbox/               # InboxPage
│   ├── today/               # TodayPage (Phase 20)
│   ├── review/              # ReviewPage (Phase 19)
│   ├── calendar/            # CalendarPage
│   ├── wiki/                # wiki tree + editor
│   ├── settings/            # SettingsHome, Backups, Bots
│   ├── agents/              # AgentsPage
│   ├── notifications/       # NotificationsBell + WS subscriber
│   ├── search/              # Cmd+K palette
│   └── layout/              # AppLayout, AppTopBar, ProjectSidebar
├── shared/
│   ├── api/client.ts        # axios instance + typed api methods
│   ├── ws.ts                # ws singleton + reconnect logic
│   ├── hooks/               # useWebSocketConnection, useBodyScrollLock, ...
│   ├── ui/                  # shadcn-style primitives
│   └── constants.ts
├── e2e/                     # Playwright specs
└── styles.css               # Tailwind entrypoint
```

Conventions:

- All API calls go through `web/src/shared/api/client.ts`. Direct
  `axios.get(...)` in components is forbidden — every endpoint has a
  typed method on `api`.
- The WS singleton (`shared/ws.ts`) is mounted once in `AppLayout`.
  All features just attach `(ws) => ws.on(topic, invalidate)` to it.
- Server state is TanStack Query. Client state is Zustand (theme,
  density, dialog stacks).
- React Router v6 with `loader()` data-fetching for the
  reason-loaders (auth gates). Pre-RQ v5 pattern.

## 9. Build pipeline

```
make dev            air (Go hot-reload)   on :2137
                    + npm run dev (Vite)  on :5173 → proxy → :2137

make build          npm run build → web/dist/* (Vite production)
                    rsync dist → internal/embed/web/dist/  (`embed-dists` target)
                    CGO_ENABLED=0 go build -ldflags "-X main.version=$(git describe --tags ...)"
                    → ./bin/orenda   (SPA embedded via //go:embed all:dist)

make test           go test ./...
                    npx vitest run

make test-e2e       make build && playwright test  (test on port 21371)

make lint           golangci-lint run (Go) + npm run lint (ESLint) + npm run format:check
```

The `embed-dists` target is what makes the production binary
self-contained — pre-27.1 the binary needed `web/dist/` on disk.

## 10. Configuration

Two layers:

1. **YAML config** (`data/config.yaml`, optional) for ops-time settings.
2. **Environment variables** with the `ORENDA_<SECTION>__<KEY>` prefix
   for shell-driven deployment. Double underscore → nested key.

By precedence: env > yaml > defaults. The `internal/config/config.go`
loader handles both. The section name in YAML must match the section
name in the env prefix (Phase 28.8 found a `rate_limit` section
+ `ORENDA_RATELIMIT_*` env bug where the env was silently ignored
when the section name didn't tokenise the same way).

### 10.1. Notable knobs

| Knob | Section | Default | Phase |
|---|---|---|---|
| `auth.jwt_ttl` | `auth` | `24h` | 28.4 |
| `auth.cookie_secure` | `auth` | `false` (loopback) | 28.4 |
| `ratelimit.auth.burst` / `auth.refill_per_sec` | `ratelimit` | `60 / 20` | 28.8 |
| `ratelimit.anon.burst` / `anon.refill_per_sec` | `ratelimit` | `20 / 5` | 28.8 |
| `server.debug_pprof` | `server` | `false` | 28.6 |
| `server.pprof_addr` | `server` | `127.0.0.1:6060` | 28.6 |
| `backup.remote_url` / `enabled` | `backup` (DB override since 28.1) | from YAML | 28.1 + 28.9 (hot reload) |

`backup.remote_url` and `backup.enabled` are the only fields that
override from the DB (Phase 28.1) and now hot-reload into the live
`*backup.Service` (Phase 28.9). Other `backup.*` knobs (mirror dir,
snapshot dir, rotation days) still require a restart — they're
tied to file system paths the Service opens at `New()`.

## 11. Security model

- All routes except `GET /healthz`, `GET /api/v1/info`,
  `GET /api/v1/openapi.yaml`, `POST /api/v1/auth/login`, and the
  webhook bot endpoints require auth.
- Passwords: bcrypt cost 12 (`internal/auth/password.go`).
- API tokens: opaque random base64url(32), stored as bcrypt.
- File uploads: MIME allowlist (`config.upload.allowed_mime_types`)
  + filename sanitization; storage under
  `data/uploads/YYYY/MM/{uuid}-{sanitized}`.
- Markdown rendering: `DOMPurify` (already a React dep); never
  eval'd in the Go side.
- Rate limit: token bucket per IP for anon, per JWT for authed
  routes. 429 with `Retry-After`.
- CSP: see `internal/api/security.go`. After Phase 28.10:
  no `unsafe-inline` for styles; `script-src 'self'`; HSTS
  delegated to reverse proxy.
- Webhook signatures: HMAC-SHA256, timestamp + nonce replay
  protection.

## 12. Operational concerns

### 12.1. Shutdown order

`cmd/orenda/main.go` shutdown loop calls:

1. `srv.Shutdown(ctx)` — drains HTTP requests (graceful
   TimeoutShutdown).
2. `botRegistry.List()` — calls `Stop(shutdownCtx)` on every
   registered bot (Phase 28.5 closes the pre-existing SIGKILL).
3. `pprofSrv.Shutdown(ctx)` — tears down the opt-in profiler
   listener (Phase 28.6).

Best-effort: a bot that fails `Stop()` logs and lets the loop
continue. Tested in `internal/bot/bot_test.go`.

### 12.2. Backup loop interactions

The backup scheduler tick (5 min) reads `*backup.Service` config via
`getCfg()` — the `atomic.Pointer[Config]` indirection added in
Phase 28.9. After Phase 28.9, every push reads the live config, so a
PUT to `/api/v1/backups/settings` is observable on the very next
push without a restart.

The pre-28.9 contract was "settings take effect after restart";
the Phase 28.1 UI showed a yellow "Restart Orenda to apply the new
remote" banner. The Phase 28.9 commit replaced that banner with
"The next push will use the new remote".

### 12.3. Local-only deployment guarantee

The deployment model: one user, one machine, one binary. No
multi-tenancy, no remote database. The license file is MIT and
the install script (`scripts/install.sh`) creates a `systemd --user`
unit that the running user owns.

## 13. Where to start reading

When you're new to the codebase:

1. **Backend hot path:** read `cmd/orenda/main.go::runServe` —
   shows how everything is wired in composition-root style.
2. **Task lifecycle:** read `internal/service/task/` — covers claim,
   submit, review, move, dependencies, status/priority/assignee.
3. **Real-time:** read `internal/api/ws/hub.go` and one subscriber
   in `internal/api/handlers_phase3.go`.
4. **Frontend entrypoint:** read `web/src/App.tsx` (routes) →
   `features/layout/AppLayout.tsx` (WS mount) →
   `features/kanban/KanbanBoard.tsx`.
5. **Database schema:** read `internal/storage/sqlite/migrations/001_init.sql`
   top-to-bottom — every table the system relies on has its root there
   even if later migrations added columns.

For AI agents and contributed PRs:

1. Read `docs/PLAN.md` for the phase definition you're working on.
2. Read this file (`docs/ARCHITECTURE.md`) for the surrounding
   constraints.
3. Skim `docs/CONTEXT.md` for the product vocabulary (kanban,
   delegation, inbox-as-non-project, course as LMS).
4. Always work in a worktree. The `main` checkout is someone else's
   live workspace.
