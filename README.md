# Orenda

> **Local-first productivity suite** where AI-agents are first-class citizens. Tasks, projects, calendar, knowledge base — everything in your life, on your machine.

*Имя — от ирокезского «orenda» — внутренняя сила, пронизывающая всё сущее.*

## Why Orenda?

В стандартных task-менеджерах AI — внешний инструмент, приклеенный через интеграции. В Orenda агенты — **полноправные участники workflow**: создают задачи, берут в работу, оставляют комментарии, получают контекст от владельца. Человек — владелец, ревьюер, инициатор.

## Stack

- **Backend:** Go 1.22+ (chi, modernc.org/sqlite, JWT, gorilla/websocket, cobra)
- **Frontend:** React 18 + TypeScript + Vite + Tailwind + shadcn/ui
- **DB:** SQLite (WAL, FTS5, pure-Go via modernc.org/sqlite, no CGO)
- **Backup:** git mirror + sqlite snapshots (configurable remote; hot-reloadable since 28.9)
- **Notifications:** Pluggable bots (VK, Telegram, Email, Webhook, Console)
- **Realtime:** WebSocket hub (cookie-auth, 8 topics) + long-poll fallback for agents
- **Agent DX:** REST + MCP server (Streamable HTTP) + `orenda agent` cobra CLI
- **Security defaults:** bcrypt-12 passwords, opaque API tokens, JWT cookie 24h, rate-limited, CSP-locked, opt-in pprof on 127.0.0.1 only
- **PWA:** Workbox service worker, IndexedDB outbox, `/api/v1/sync` flush

## Quickstart

```bash
# Install deps
make web-install

# Build and run
make build
./bin/orenda migrate up
echo "hunter2!" | ./bin/orenda user create \
    --email you@example.com --display-name You --password-stdin \
    --config data/config.yaml
ORENDA_AUTH__JWT_SECRET=$(head -c32 /dev/urandom | base64) ./bin/orenda serve
# → http://127.0.0.1:2137
```

Or one-shot install:

```bash
make web-install               # required once — the installer builds the SPA
scripts/install.sh --systemd   # builds, installs to ~/.local/bin, enables user service
```

> `scripts/install.sh` is the **only** sanctioned way to update the
> usage binary. It refuses to install from anything except a clean
> checkout on `main` (override with `--force`). See
> [docs/ARCHITECTURE.md §12.4](docs/ARCHITECTURE.md#124-dev-vs-dogfood-instance-phase-2820).

For development with hot reload:

```bash
make dev
# → Vite dev-server: http://localhost:5173 (proxies API to :2138)
# → Go server: http://127.0.0.1:2138
```

> Phase 28.20 splits dev (`:2138`) and usage (`:2137`) so both can run on
> the same machine. The usage/dogfood instance is built from a separate
> checkout on `main`; see [docs/ARCHITECTURE.md §12.4](docs/ARCHITECTURE.md#124-dev-vs-dogfood-instance-phase-2820)
> for the channel model and `scripts/update-dogfood.sh` for the
> one-command refresh.

Validate the codebase before opening a PR:

```bash
make test              # Go + vitest (246 tests, ~5s)
make lint              # golangci-lint + eslint
make web-format-check  # prettier check (not part of make lint)
make test-e2e          # Playwright against a fresh embedded build on :21371 (18 tests / 13 specs)
make govulncheck       # Go vulnerability DB scan
```

A `pre-commit` hook is installed via `simple-git-hooks` on
`npm install` in `web/` — it auto-formats staged `.ts/.tsx/.css` files
with Prettier before each commit. Skip with
`SKIP_SIMPLE_GIT_HOOKS=1 git commit ...` when you're mid-edit.

## Features

- 📋 Projects, boards, kanban with drag-and-drop, columns-as-statuses (Phase 27.8)
- ✅ Tasks with statuses (backlog → todo → in_progress → review → done)
- 🤖 AI-agents with API tokens, atomic claim, heartbeat, blocked-by-graph
- 💬 Comments, attachments, mentions between user and agents, agent-author audit
- 📅 Calendar (events + tasks with due dates, RRULE expansion, WIP limits)
- 📚 Wiki with markdown, wiki-links, backlinks, FTS5 BM25 search
- 🎓 Personal LMS courses — built by an AI tutor or by hand (LessonPage, quizzes exact/open)
- 🔍 Review queue — agent work awaiting your decision, one click away
- ⏱️ Time tracking with timer + manual entries, /today driver page
- 🔔 Pluggable notifications (VK, Telegram, Email, Webhook, Console)
- 💾 Git-based backups (GitHub, Bitbucket, SourceCraft, custom) + sqlite .backup + WAL archive + UI restore
- 📱 PWA (offline-first) — IndexedDB outbox, sync flush
- ⚡ Live UI updates via WebSocket on 8 topics (tasks, agents, attachments, comments, events, notifications, timers, wiki)
- 🔐 Two parallel auth models: cookie JWT (UI) vs Bearer API-token (agents)
- 🛠️ `orenda agent` CLI + MCP server (Streamable HTTP) for tool-using agents

## Documentation

- [PRD](docs/PRD.md) — Product Requirements Document (vision)
- [PLAN](docs/PLAN.md) — Development phases and tasks
- [ARCHITECTURE](docs/ARCHITECTURE.md) — what's in the binary, data-flow reference
- [CONTEXT](docs/CONTEXT.md) — Domain concepts (kanban, courses, delegation)
- [API](docs/API.md) — REST API reference (+ [openapi.yaml](docs/openapi.yaml))
- [DB](docs/DB.md) — Database schema (per migration)
- [SESSION](docs/SESSION.md) — session snapshot (current state, recent decisions)
- [AGENTS.md](AGENTS.md) — guidelines for AI agents extending the codebase
- [SKILL](docs/skills/orenda/SKILL.md) — agent workflow + etiquette

## Roadmap

| Phase | Status | Description |
|-------|--------|-------------|
| 0 — Init | ✅ | Project skeleton, healthcheck |
| 1 — Core | ✅ | Users, auth, projects, tasks CRUD |
| 2 — Kanban | ✅ | Boards, drag-and-drop, WS |
| 3 — Agents + Collaboration | ✅ | Agent API, comments, mentions, long-poll |
| 4 — Calendar + Time | ✅ | Events, recurrence, timer |
| 5 — Wiki + Search | ✅ | Pages, wiki-links, FTS5 |
| 6 — Notifications (facade) | ✅ | In-app + bot abstraction |
| 7 — Backups | ✅ | Git mirror + sqlite snapshots + restore |
| 8 — PWA | ✅ | Offline support, IndexedDB outbox, /sync flush |
| 9 — Polish (initial) | ✅ | Tests, security headers, installer, dark mode |
| 10 — Bot platform | ✅ | VK, Telegram, Email, Webhook |
| 11–27 | ✅ | Projects UI, kanban columns, tags, dependencies, inbox, rich cards, LMS courses, review queue, today, quick capture, restore, OpenAPI, agent CLI + MCP, E2E suite — see [PLAN](docs/PLAN.md) |
| 28 (Polish backlog close-out) | ✅ | Settings hub, TaskModal scroll, security defaults (JWT 24h, Secure from config), activity emission, Bot.Stop on shutdown, opt-in pprof, govulncheck target, Prettier, hot-reload backup, CSP tightening, ARCHITECTURE.md |
| 30.1 (CI) | ✅ | GitHub Actions: `lint` → `test` → `build` → `e2e`. PR gate is incremental (`--new-from-merge-base`); release branch (`main`) gets full lint; 73 pre-existing lint issues remain (see [PLAN](docs/PLAN.md) §30.16) |
| 30.2 (sync_ops observability) | ✅ | `sync_ops.Record()` failures now bump `sync_ops_record_failures` in `/api/v1/stats` and emit a `zap.Warn` with client/server ids — no more silent PWA outbox replay loop |

> Screenshots: not bundled in the repo (kept light — no binary blobs).
> Run `make build && bin/orenda serve` and visit the four key pages:
> `/`, `/inbox`, `/courses`, `/settings` to see the current UI.

## License

MIT (TBD)