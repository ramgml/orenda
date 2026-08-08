# Orenda

> **Local-first productivity suite** where AI-agents are first-class citizens. Tasks, projects, calendar, knowledge base — everything in your life, on your machine.

*Имя — от ирокезского «orenda» — внутренняя сила, пронизывающая всё сущее.*

## Why Orenda?

В стандартных task-менеджерах AI — внешний инструмент, приклеенный через интеграции. В Orenda агенты — **полноправные участники workflow**: создают задачи, берут в работу, оставляют комментарии, получают контекст от владельца. Человек — владелец, ревьюер, инициатор.

## Stack

- **Backend:** Go 1.22+ (chi, modernc.org/sqlite, JWT, gorilla/websocket, cobra)
- **Frontend:** React 18 + TypeScript + Vite + Tailwind + shadcn/ui
- **DB:** SQLite (WAL, FTS5)
- **Backup:** git mirror + sqlite snapshots (configurable remote)
- **Notifications:** Pluggable bots (VK, Telegram, Email, Webhook, Console)

## Quickstart

```bash
# Install deps
make web-install

# Build and run
make build
make migrate-up
./bin/orenda user create --email you@example.com --password yourpass
./bin/orenda serve
# → http://127.0.0.1:2137
```

For development with hot reload:

```bash
make dev
# → Vite dev-server: http://localhost:5173 (proxies API to :2137)
# → Go server: http://127.0.0.1:2137
```

## Features

- 📋 Projects, boards, kanban with drag-and-drop
- ✅ Tasks with statuses (backlog → todo → in_progress → review → done)
- 🤖 AI-agents with API tokens, atomic claim, heartbeat
- 💬 Comments, attachments, mentions between user and agents
- 📅 Calendar (events + tasks with due dates)
- 📚 Wiki with markdown, wiki-links, backlinks, FTS5 search
- ⏱️ Time tracking with timer + manual entries
- 🔔 Pluggable notifications (VK, Telegram, Email, Webhook, Console)
- 💾 Git-based backups (GitHub, Bitbucket, SourceCraft, custom)
- 📱 PWA (offline-first)

## Documentation

- [PRD](docs/PRD.md) — Product Requirements Document
- [PLAN](docs/PLAN.md) — Development phases and tasks
- [AGENTS.md](AGENTS.md) — Guidelines for AI agents working on this codebase

## Roadmap

| Phase | Status | Description |
|-------|--------|-------------|
| 0 — Init | 🚧 | Project skeleton, healthcheck |
| 1 — Core | ⏳ | Users, auth, projects, tasks CRUD |
| 2 — Kanban | ⏳ | Boards, drag-and-drop, WS |
| 3 — Agents + Collaboration | ⏳ | Agent API, comments, mentions, long-poll |
| 4 — Calendar + Time | ⏳ | Events, recurrence, timer |
| 5 — Wiki + Search | ⏳ | Pages, wiki-links, FTS5 |
| 6 — Notifications (facade) | ⏳ | In-app + bot abstraction |
| 7 — Backups | ⏳ | Git mirror + sqlite snapshots |
| 8 — PWA | ⏳ | Offline support, IndexedDB outbox |
| 9 — Polish | ⏳ | Tests, docs, installer |
| 10 — Bot platform | ⏳ | VK, Telegram, Email, Webhook |

## License

MIT (TBD)