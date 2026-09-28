# Orenda

> **Local-first productivity suite** where AI-agents are first-class citizens. Tasks, projects, calendar, knowledge base — everything in your life, on your machine.

*The name comes from the Iroquoian "orenda" — the inner force that pervades all being.*

[Русский](README.ru.md)

## Why Orenda?

In standard task managers AI is an external tool bolted on through integrations. In Orenda agents are **full-fledged workflow participants**: they create tasks, claim work, leave comments, receive context from the owner. The human is the owner, the reviewer, the initiator.

## Stack

- **Backend:** Go 1.22+ (chi, modernc.org/sqlite, JWT, gorilla/websocket, cobra)
- **Frontend:** React 18 + TypeScript + Vite + Tailwind + shadcn/ui
- **DB:** SQLite (WAL, FTS5, pure-Go via modernc.org/sqlite, no CGO) — default; PostgreSQL via the same binary (`storage.driver=postgres`: embedded / external / Docker, wiki T360–T367)
- **Backup:** git mirror + dialect-aware snapshots (sqlite `VACUUM INTO` / `pg_dump -Fc`; configurable remote; hot-reloadable since 28.9)
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
> [docs/context/ARCHITECTURE.md §12.4](docs/context/ARCHITECTURE.md#124-dev-vs-dogfood-instance-phase-2820).

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
> checkout on `main`; see [docs/context/ARCHITECTURE.md §12.4](docs/context/ARCHITECTURE.md#124-dev-vs-dogfood-instance-phase-2820)
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

### Database backends: SQLite or PostgreSQL

Orenda ships two storage drivers behind one repository seam
([wiki:storage-adapters](http://localhost:2137/wiki/storage-adapters), T360–T367):
**SQLite** (default — behaviour unchanged) and **PostgreSQL** (opt-in via
config). The schema, migrations, backup and full-text search follow the
driver automatically; the same single static binary serves both and stays
`CGO_ENABLED=0` (SQLite via pure-Go `modernc.org/sqlite`, PostgreSQL via
pure-Go `jackc/pgx`).

```yaml
storage:
  driver: sqlite          # "sqlite" (default) | "postgres"
  data_dir: data
  db_path: data/orenda.db
  wal_mode: true
  busy_timeout_ms: 5000   # sqlite: busy_timeout; postgres: session lock_timeout
  enable_foreign_keys: true
  postgres:
    dsn: ""               # libpq DSN — wins over all parts below (T363)
    host: 127.0.0.1       # used when dsn is empty
    port: 5432
    user: ""
    password: ""
    database: ""          # required for driver=postgres (both modes)
    ssl_mode: ""          # libpq default "prefer"; embedded pins "disable"
    embedded: false       # true → run a local cluster (zero-setup mode)
    embedded_port: 5433
    binaries_url: ""      # Maven mirror for embedded binaries (offline setups)
    dump_bin: ""          # pg_dump override for backup (bare name → PATH)
```

Every key has an env override (`__` separates sections, lowercase —
[`ORENDA_STORAGE__*`](docs/context/DB.md#configuration-reference)):

| Env | Default | Meaning |
|---|---|---|
| `ORENDA_STORAGE__DRIVER` | `sqlite` | `sqlite` or `postgres` |
| `ORENDA_STORAGE__DATA_DIR` | `data` | runtime dir root (embedded PGDATA lives in `<data_dir>/postgres`) |
| `ORENDA_STORAGE__DB_PATH` | `data/orenda.db` | sqlite file path |
| `ORENDA_STORAGE__WAL_MODE` | `true` | sqlite WAL journal |
| `ORENDA_STORAGE__BUSY_TIMEOUT_MS` | `5000` | sqlite `busy_timeout` / postgres `lock_timeout` |
| `ORENDA_STORAGE__ENABLE_FOREIGN_KEYS` | `true` | sqlite `foreign_keys` pragma |
| `ORENDA_STORAGE__POSTGRES__DSN` | — | libpq DSN; **overrides all parts below** |
| `ORENDA_STORAGE__POSTGRES__HOST` | `127.0.0.1` | part form only |
| `ORENDA_STORAGE__POSTGRES__PORT` | `5432` | part form only |
| `ORENDA_STORAGE__POSTGRES__USER` | — | embedded default: `postgres` |
| `ORENDA_STORAGE__POSTGRES__PASSWORD` | — | embedded default: `postgres` |
| `ORENDA_STORAGE__POSTGRES__DATABASE` | — | required when driver=postgres (embedded included) |
| `ORENDA_STORAGE__POSTGRES__SSL_MODE` | `prefer` | libpq `sslmode`; embedded cluster pins `disable` |
| `ORENDA_STORAGE__POSTGRES__EMBEDDED` | `false` | run the local embedded cluster |
| `ORENDA_STORAGE__POSTGRES__EMBEDDED_PORT` | `5433` | embedded cluster port |
| `ORENDA_STORAGE__POSTGRES__BINARIES_URL` | Maven Central | mirror repo for the embedded runtime's postgres binaries |
| `ORENDA_STORAGE__POSTGRES__DUMP_BIN` | PATH lookup | `pg_dump` for backup/restore (T366) |

**Three ways to run PostgreSQL** (embedded and external share one code path — D4/D7):

1. **Embedded** — zero-setup local mode:

   ```bash
   ORENDA_STORAGE__DRIVER=postgres \
   ORENDA_STORAGE__POSTGRES__EMBEDDED=true \
   ORENDA_STORAGE__POSTGRES__DATABASE=orenda \
   ORENDA_AUTH__JWT_SECRET=$(openssl rand -hex 32) ./bin/orenda serve
   ```

   The binary owns a throwaway PostgreSQL 16 cluster (`fergusstrange/embedded-postgres`,
   zonky binaries): `serve`, `migrate` and `user` commands start it, shutdown stops it.
   Data lives in `data/postgres/` (PGDATA, survives restarts; the runtime scratch
   dir is outside and wiped per start), the postmaster listens on `127.0.0.1:5433`,
   logs go into the structured zap log. On first start the binaries (~15 MB
   compressed) are downloaded from Maven Central and cached in
   `~/.embedded-postgres-go` — offline hosts can point `binaries_url` at an
   internal Maven mirror or pre-warm the cache.
   The cluster locale is pinned to `C.UTF-8` (fallback `en_US.UTF-8`) so
   case-folding search works regardless of the host `LANG`; a C-locale cluster is
   rejected loudly at startup, not silently.

2. **External server** — point at an existing PostgreSQL (LAN, VPS, managed):

   ```bash
   ORENDA_STORAGE__DRIVER=postgres \
   ORENDA_STORAGE__POSTGRES__DSN="postgres://orenda:secret@db.lan:5432/orenda?sslmode=require" \
   ./bin/orenda migrate up
   ```

   `dsn` wins over the individual parts; without it, `host/port/user/password/database/ssl_mode`
   are assembled (defaults `127.0.0.1:5432`, `sslmode=prefer`). **sslmode note:** for
   remote servers set `ssl_mode=require` (or `verify-full` with a CA bundle for the
   public internet) — the libpq default `prefer` encrypts but does not authenticate
   the server; the embedded cluster runs loopback-only with `sslmode=disable`.

3. **Docker** — a stock `postgres:16` next to the app (the shipped `docker-compose.yml`
   keeps the app on SQLite; add the database container yourself):

   ```bash
   docker run -d --name orenda-pg -e POSTGRES_USER=orenda -e POSTGRES_PASSWORD=secret \
     -e POSTGRES_DB=orenda -p 5432:5432 -v orenda-pgdata:/var/lib/postgresql/data postgres:16-alpine
   # then start orenda with ORENDA_STORAGE__POSTGRES__DSN=postgres://orenda:secret@127.0.0.1:5432/orenda?sslmode=disable
   ```

   (Inside a compose network dial the service name, not `127.0.0.1`, and keep
   `sslmode=disable` on the private network.)

**Port map:** `2137` usage server · `2138` dev server · `21371` E2E ·
`21400–21499` QA preview instances · `5432` default PostgreSQL · `5433`
embedded cluster (chosen to clear a developer's `5432`).

**Backup and maintenance follow the driver** (T366): snapshots on postgres are
`pg_dump --format=custom` archives in the same `data/snapshots/` directory with
the same naming and rotation; `orenda backup restore` restores into a scratch
database, verifies (`pg_restore --list` + applied-migration check) and drops it —
`--to <database>` promotes instead. The maintenance "verify" step is per-dialect:
sqlite runs `integrity_check` + `foreign_key_check`, postgres runs the scratch
restore. **Honest tooling contract:** `pg_dump`/`pg_restore` are client tools and
are *not* part of the embedded bundle (it ships server binaries only) — the
backup resolves them via `storage.postgres.dump_bin` → extracted
embedded-runtime dirs (usually nothing there) → `PATH`; in practice install
`postgresql-client` so `pg_dump` is on `PATH`, or point `dump_bin` at a
concrete binary. The backup error names both options when the tool is
missing, and CLI backup commands against an embedded cluster require the
server to be up — it owns the postmaster.

**Known limitations (2026-09):**

- Ranking **numbers** differ between engines (sqlite FTS5 `bm25` vs postgres
  `ts_rank` — term frequency, no IDF); hit sets and sanity ordering match
  (PR #266). Neither engine stems — «поиск» does not match «поиска».
- Embedded-locale policy is pinned to `C.UTF-8`/`en_US.UTF-8`; a
  cross-platform locale matrix is tracked as T368.
- Embedded dump tooling relies on `PATH`/`dump_bin` (see contract above).
- There is **no automatic sqlite → postgres data migration** — switching an
  existing install's driver (e.g. the dogfood `:2137` instance) is an open
  owner decision, not part of this epic.

**QA preview convention:** a preview instance that points at a shared
`PGDATA` (embedded mode on a QA box) drops a `PREVIEW_OWNER` marker file
into the data directory naming the instance/branch that owns the cluster —
before wiping or re-initializing shared postgres data, check the marker
first (after the T365 QA preview incident).

### Run in Docker

Docker is an additional delivery channel; the canonical path is systemd
(`scripts/install.sh --systemd`, see above).

```bash
# Build the image (multi-stage: node SPA → Go binary with the SPA embedded → alpine runtime)
docker build -t orenda:local .

# Run: the host port is set via ORENDA_PORT; the secret is required (compose
# refuses to start without it — the error message says so; the variable is
# needed for `docker compose exec`/`logs` too)
ORENDA_PORT=8080 ORENDA_AUTH__JWT_SECRET=$(openssl rand -hex 32) docker compose up -d --build
# → http://127.0.0.1:8080
```

- **Data:** named volume `orenda-data` → `/app/data` (SQLite + WAL);
  `docker compose down` keeps it, the next `up` picks the database up.
- **First user:**

  ```bash
  echo "your-password" | docker compose exec -T app orenda user create \
      --email you@example.com --display-name You --password-stdin
  ```

- **Backup:** `docker compose exec app orenda backup snapshot` writes a
  sqlite snapshot into the volume (`/app/data/snapshots/`). `orenda backup
  push` (git) works too — git ships in the image, but the remote and its
  credentials must be configured from inside the container yourself.
- **Upgrade:** `docker compose down`, then `ORENDA_PORT=…
  ORENDA_AUTH__JWT_SECRET=… docker compose up -d --build` again — data in
  the volume survives the container recreation.

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
