# Orenda

> **Local-first productivity suite** where AI-agents are first-class citizens. Tasks, projects, calendar, knowledge base — everything in your life, on your machine.

*The name comes from the Iroquoian "orenda" — the inner force that pervades all being.*

[Русский](README.ru.md)

## Why Orenda?

In standard task managers AI is an external tool bolted on through integrations. In Orenda agents are **full-fledged workflow participants**: they create tasks, claim work, leave comments, receive context from the owner. The human is the owner, the reviewer, the initiator.

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

# Alternative (Task 138): keep the secret out of /proc/*/environ —
# write it to a file once, then point ORENDA_AUTH__JWT_SECRET_FILE at it
# (direct ORENDA_AUTH__JWT_SECRET still wins when both are set):
printf '%s' "$(head -c32 /dev/urandom | base64)" > data/credentials/jwt
ORENDA_AUTH__JWT_SECRET_FILE=$PWD/data/credentials/jwt ./bin/orenda serve
```

Or one-shot install:

```bash
make web-install               # required once — the installer builds the SPA
scripts/install.sh --systemd   # builds, installs to ~/.local/bin, enables user service
```

### Install via AI agent (prompt)

Paste this into your AI coding agent (Claude, Codex, Cursor, …) to have it install and set up Orenda for you:

```text
Install Orenda (https://github.com/ramgml/orenda) on this machine:
1. Clone the repo into ~/opt/orenda and checkout the latest release tag (git describe --tags --abbrev=0 on origin/main).
2. Run `make web-install` (Node.js >= 24.11 required) to build the web SPA.
3. Run `make build` to produce ./bin/orenda.
4. Run `./bin/orenda migrate up`.
5. Create an admin user: `echo "<password>" | ./bin/orenda user create --email <email> --display-name <name> --password-stdin --config data/config.yaml`.
6. Start the server with a generated JWT secret:
   ORENDA_AUTH__JWT_SECRET=$(head -c32 /dev/urandom | base64) ./bin/orenda serve
8. Verify: `curl -s http://127.0.0.1:2137/healthz` (or open http://127.0.0.1:2137 in a browser) and confirm the login page loads.
Do not edit files inside data/ by hand; use the CLI commands above.
```

> `scripts/install.sh` is the **only** sanctioned way to update the
> usage binary. It refuses to install from anything except a clean
> checkout on `main` (override with `--force`). See
> [docs/ARCHITECTURE.md §12.4](docs/ARCHITECTURE.md#124-dev-vs-dogfood-instance-phase-2820).

### Windows

Orenda builds and runs natively on Windows. SQLite is pure-Go
(`modernc.org/sqlite`, no CGO), so no C toolchain is needed.

**Native build:**

```powershell
git clone https://github.com/ramgml/orenda ~/opt/orenda
cd ~/opt/orenda
git checkout v0.14.0            # latest release tag
make web-install                # Node.js >= 24.11 required
make build                      # produces bin\orenda.exe
.\bin\orenda.exe migrate up
"your-password" | .\bin\orenda.exe user create `
    --email you@example.com --display-name You --password-stdin
$env:ORENDA_AUTH__JWT_SECRET = [Convert]::ToBase64String((1..32 | ForEach-Object { Get-Random -Maximum 256 }))
.\bin\orenda.exe serve          # → http://127.0.0.1:2137
```

To run it as a background service, wrap `orenda serve` in a Windows
service (e.g. [WinSW](https://github.com/winsw/winsw)) or a Task
Scheduler job — `scripts/install.sh` is Unix/systemd-only.

**Cross-compile** from any Unix box:

```bash
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -o orenda.exe ./cmd/orenda
```

**WSL2:** follow the standard Linux quickstart inside WSL —
`http://127.0.0.1:2137` is reachable from Windows browsers.

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
make test              # Go + vitest (cached, fast)
make test-full         # Full uncached run (CI backstop / release gate)
make lint-new          # golangci-lint on NEW code only (what pre-push gates)
make lint              # full lint (golangci-lint + eslint) — surfaces pre-existing debt
make test-e2e          # Playwright against a fresh embedded build on :21371 (18 tests / 13 specs)
make govulncheck       # Go vulnerability DB scan
```

**Local gates are git hooks (Phase 32.6).** Install once per clone
(idempotent — safe to re-run):

```bash
make hooks   # sets core.hooksPath = scripts/git-hooks (shared git config;
             # all current and future worktrees inherit it)
```

After that, every `git commit` runs `pre-commit` (`gofmt -l` +
`prettier --check` on staged files, <2 s) and every `git push` runs
`pre-push` (`make lint-new` + `make test`, ~1 min). `--no-verify` is
forbidden; use `SKIP_ORENDA_HOOKS=1` only for explicit, named
exceptions. See [AGENTS.md](AGENTS.md#local-gates--git-hooks-phase-326)
and the [ci-local-gates-hooks](http://localhost:2137/wiki/ci-local-gates-hooks)
wiki page.

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

> Screenshots: not bundled in the repo (kept light — no binary blobs).
> Run `make build && bin/orenda serve` and visit the four key pages:
> `/`, `/inbox`, `/courses`, `/settings` to see the current UI.

## License

MIT (TBD)
