# AGENTS.md — Orenda Project Guidelines for AI Agents

> Краткий guide для AI-агентов, работающих с кодовой базой Orenda. Полные требования — в [[docs/PRD.md]], очередь работ и постановки — в dogfood-инстансе по конвенции [[docs/DOGFOOD.md]], архив фаз ≤ 32 — в [[docs/PLAN.md]].

## What is Orenda?

Local-first productivity suite (tasks, calendar, wiki) where **AI-agents are first-class citizens**. Single Go binary + React SPA, SQLite, port **2137** (usage/dogfood instance) / **2138** (`make dev`). Backup via git + sqlite snapshots.

## Stack

| Layer | Tech |
|-------|------|
| Backend | Go 1.22+ (chi, modernc.org/sqlite, jwt, gorilla/websocket, cobra) |
| Frontend | React 18 + TS + Vite + Tailwind + shadcn/ui (`@radix-ui/react-dialog` ^1.1.23, `class-variance-authority` ^0.7.1, `clsx` ^2.1.1, `tailwind-merge` ^3.6.0; Phase 32.13 — one `Dialog` primitive replaces the corresponding self-made modal in `Backups.tsx`) |
| DB | SQLite (WAL mode) |
| Migrations | custom runner `sqlite.Migrate`/`MigrateDown` (sequential `NNN_*.sql` / `NNN_*.down.sql`) |

## Directory map

```
orenda/
├── cmd/orenda/main.go            # Entry point + cobra commands
├── internal/
│   ├── api/                       # HTTP handlers, ws hub, middleware
│   ├── auth/                      # JWT + opaque API tokens
│   ├── backup/                    # git push + sqlite snapshot
│   ├── bot/                       # Pluggable bot interface (Phase 10)
│   ├── config/                    # yaml + env
│   ├── domain/                    # Entities + repository interfaces
│   ├── embed/web/                 # embed.FS for React build
│   ├── mcp/                       # MCP stdio server (Phase 25)
│   ├── mirror/                    # Markdown mirror for git history
│   ├── service/                   # Business logic
│   └── storage/sqlite/            # Repositories + migrations
├── web/                           # Vite + React
├── data/                          # Runtime (gitignored)
├── docs/                          # PRD, PLAN, ARCHITECTURE, API, DB
├── scripts/                       # install.sh, orenda.service
├── Makefile
├── go.mod
└── README.md
```

## Build & run

```bash
make dev              # Go (air) + Vite dev-server with proxy
make build            # ./bin/orenda with web/dist embedded
make test
make lint
./bin/orenda serve    # Production on http://127.0.0.1:2137
./bin/orenda migrate up
./bin/orenda backup push
./bin/orenda user create --email ... --display-name ... --password-stdin
./bin/orenda agent <me|next|claim|context|submit|release|comment|await>   # agent CLI (Phase 25)
./bin/orenda mcp-proxy # stdio MCP bridge (Phase 25)
```

## Local gates — git hooks (Phase 32.6)

CI is no longer the per-PR gate. Per-PR enforcement lives in **local git
hooks** (wiki:ci-local-gates-hooks). GitHub Actions runs only the release
gate (PR/push to `main`, tags `v*`) and a test-only backstop on push to
`dev`. PR-to-dev is intentionally silent — agents don't wait on CI.

Setup (one-time per clone):

```bash
make hooks   # sets core.hooksPath = scripts/git-hooks (idempotent)
```

This writes into the **shared** git config (the main checkout's `.git/`),
so all current and future worktrees inherit the hook path automatically
— no per-worktree install.

Hook contract:

| Hook        | Runs on        | Checks                                                            | Cost       |
|-------------|----------------|-------------------------------------------------------------------|------------|
| `pre-commit`| `git commit`   | `gofmt -l` on staged `.go` + `prettier --check` on staged web     | <2 s       |
| `pre-push`  | `git push`     | `make lint-new` + `make test`                                     | ~1 min     |

`make lint-new` is `golangci-lint run --new-from-merge-base=origin/dev ./...`
— exactly the gate the old PR CI used, minus the pre-existing lint debt
(see Phase 30.16). ~8.5 s warm.

Process rules (binary, like the rest of this file):

- **Agent does not wait on CI.** Open the PR, run `orenda agent submit`,
  claim the next task. A red backstop run on `dev` is fixed by a
  dedicated follow-up task — not in the working cycle.
- **`--no-verify` is forbidden.** Use `SKIP_ORENDA_HOOKS=1` if you must
  bypass (rare); the PR review will catch any skipped gate and reject it.
- **Pre-existing lint debt** (`make lint` shows ≈71 issues on `dev`) is
  not a personal failure. `lint-new` is the contract; `make lint` is the
  debt inventory (Phase 30.16 — close opportunistically).

## Coding rules

### Go
1. **Package names**: lowercase, no underscores. `task` not `task_service`.
2. **Errors**: wrap with `fmt.Errorf("op: %w", err)`. Never `panic` in business code.
3. **Context first**: every public function takes `ctx context.Context`.
4. **SQL**: prepared statements, `?` placeholders, never string concat.
5. **Migrations**: sequential numbers, never edit shipped migrations.
6. **Tests**: table-driven, `testify/assert` for readability.
7. **Imports**: stdlib, third-party, internal — three groups separated by blank line.
8. **Logging**: structured zap, never `fmt.Println` in production code.
9. **Comments**: English, doc comments on exported identifiers.

### TypeScript / React
1. **Components**: function components only. Hooks for state.
2. **State**: TanStack Query for server state, Zustand for client state.
3. **API calls**: through `web/src/shared/api/client.ts` axios instance.
4. **Routing**: react-router-dom v6+, loader pattern for data.
5. **Styling**: Tailwind classes, no inline styles except for dynamic values.
6. **Types**: strict TS, no `any` (use `unknown` + narrowing).
7. **Imports**: `@/` alias for `web/src/`.

### Database
1. **IDs**: UUIDv7 strings, not integers (sortable + URL-safe).
2. **Timestamps**: ISO 8601 strings (`datetime('now')` in SQLite).
3. **Soft delete**: `archived` flag on projects, hard delete on others.
4. **Indexes**: every FK + every query used in WHERE.

## Conventions for AI agents

### When you start a task
1. Work comes from the dogfood instance (`orenda agent next` / MCP `orenda_list_tasks`) — see `docs/DOGFOOD.md`. `docs/PLAN.md` is a frozen archive of phases ≤ 32, not a queue.
2. Check Definition of Done (in the task description / linked wiki постановка).
3. Create worktree + branch `phase-X-Y-short-name` (см. «Worktree per task» — обязательно, без исключений).
4. Implement tasks in order.
5. Run `make test && make lint`.
6. Re-index the codebase knowledge graph after code changes: codebase-memory-mcp `index_repository` with `mode: "fast"` (`"full"` on first index). Code discovery runs through `search_graph`/`trace_path` — a stale graph misleads the next agent.
7. Open PR via `.github/PULL_REQUEST_TEMPLATE.md` — it mechanically enforces the Definition of Done checklist (see next section).

### Definition of Done is binary

A task is done or not done — "almost done" is not done. Phases here have been signed off as complete while core flows were missing (Phase 18 shipped without lesson materialization, quiz answering, or any quiz-creation endpoint; the generator task stayed a placeholder nothing wrote to). These rules exist so that never happens silently again.

1. **Verify every DoD item by execution.** A test run, a smoke walkthrough, a command whose output you can quote in the PR. "Implemented" and "should work" are not verification.
2. **Report partial as partial.** If 4 of 6 DoD items pass, say exactly which 2 are missing and why. A task's DoD checkbox turns done only when every item passes — never in good faith.
3. **No silent scope reduction.** A requirement that is blocked, ambiguous, or wrong is surfaced (in the PR, or to the user) — not dropped, stubbed, or deferred to an unrecorded "later".
4. **No stubs in delivered code.** No `TODO: implement`, no fields nothing writes to, no dead endpoints. An intentionally deferred seam is filed as a task in the dogfood instance (постановка в wiki, если решение нетривиально), so the next agent sees it.
5. **Self-review against the DoD before opening the PR.** Walk it top to bottom; attach one evidence line per item. An item without evidence means the PR is not ready.

### When you're stuck
- Read [[docs/PRD.md]] for intent.
- Read [[docs/ARCHITECTURE.md]] (when exists) for design.
- Search the codebase with `grep` or `glob` before asking.
- Look at neighbouring code for conventions.

### What NOT to do
- ❌ Don't edit shipped migrations. Add new ones.
- ❌ Don't introduce new dependencies without justification.
- ❌ Don't refactor unrelated code in your PR.
- ❌ Don't add `any` types in TS.
- ❌ Don't bypass auth "temporarily".
- ❌ Don't commit `data/` contents (gitignored).
- ❌ Don't edit the main working tree directly — worktree per task, always. Other agents may hold uncommitted work there; they don't know about you either.
- ❌ Don't run `git checkout` / `git reset` / `git clean` / `git restore` in a checkout you didn't create.
- ❌ Don't push to remote without explicit user request.
- ❌ Don't bypass git hooks with `git commit --no-verify` or `git push --no-verify`. Use `SKIP_ORENDA_HOOKS=1` only for explicit, named exceptions and surface them in the PR.
- ❌ Don't wait on GitHub Actions for a PR into `dev` — PR-to-dev is intentionally silent (wiki:ci-local-gates-hooks). Open the PR, submit, claim next task.
- ❌ Don't report unverified or incomplete work as done. Partial delivery is reported as partial, with the missing items named — see «Definition of Done is binary».

## Key files to read first

- `docs/DOGFOOD.md` — **agent entry point** (where work comes from: the dogfood instance, not files; task workflow + review loop)
- `docs/CONTEXT.md` — **domain context** (what kanban / courses / delegation ARE — shared mental models that prevent wrong reinvention; concepts, not rules — read second)
- `docs/PRD.md` — what we're building and why
- `docs/PLAN.md` — phases ≤ 32 archive (task definitions, audits, known gaps); the live queue is the dogfood instance per DOGFOOD.md
- `internal/storage/sqlite/migrations/001_init.sql` — DB schema *(Phase 1)*
- `internal/config/config.go` — config structure *(Phase 0)*
- `cmd/orenda/main.go` — entry point and CLI *(Phase 0)*

## Communication

- Comments in code: **English**.
- Commit messages: `phase(X.Y): short description` (e.g., `phase(1.3): add task repository`).
- PR titles: `[Phase X.Y] short description`.
- Issue references: `closes #N` or `refs PRD#section`.

## Git workflow

- `main` — stable releases only. Tagged `vX.Y.Z`. No direct commits.
- `dev` — active development. Default branch for feature work.
- `phase-X-Y-<name>` — feature branches off `dev`. One branch per PLAN task.
- Merge to `dev` after review. Tag phase milestone: `git tag v0.1.0-phaseX`.
- Promote to `main` via PR from `dev` when ready. Tag release: `git tag vX.Y.Z`.
- See `CHANGELOG.md` for versioning policy and release notes.

### Worktree per task (mandatory, unconditional)

Agents run in parallel and **cannot see each other**. You cannot know whether another agent is working right now — assume there is always one. Therefore every task, even a one-file docs change, gets its own worktree. The main checkout is someone else's live workspace.

```bash
# Start of task: branch + worktree off dev, nested under .worktrees/
git worktree add .worktrees/<task> -b phase-X-Y-<name> dev

# ...work in .worktrees/<task>...

# Commit early, commit often: uncommitted work is unprotected —
# another agent's tree operation can silently destroy it.
git add -A && git commit -m "phase(X.Y): ..."

# After merge to dev:
git worktree remove .worktrees/<task> && git worktree prune
```

Rules:

- **The main checkout is read-only for you.** No edits there; no `git checkout` / `git reset` / `git clean` / `git restore` in any checkout you didn't create. Inspecting files read-only is fine.
- One branch = one worktree. A branch cannot be checked out in two places; create the task branch with `git worktree add -b`.
- Placement: **always nested `.worktrees/<task>`** — never a sibling `../` directory next to the repo. This is safe **only because** `.worktrees/` is in `.gitignore`: the leading dot keeps Go tooling out (`go test ./...` skips dot-dirs) and gitignore keeps `git add -A`, search and watchers clean. Any other location is forbidden: sibling checkouts pollute the shared parent directory seen by every project and agent, and an unignored nested checkout breaks the main tree (embedded-repo index garbage, duplicate module builds, watcher storms).
- Gitignored content is not copied. In a fresh worktree run `npm install` in `web/` and `./bin/orenda migrate up` (each worktree gets its own `data/orenda.db`).
- **Ports:** `:2137` is reserved for the usage/dogfood systemd instance (from `~/opt/orenda`). `:2138` is the dev default (`make dev`); `:21371` is E2E. Don't run two instances on the same port; pick one or set `ORENDA_SERVER__PORT` to something free. Phase 28.20 split these so dev and usage can co-exist.
- Merge to `dev` is a deliberate act: only after review, and never against someone's uncommitted work in the main tree.
- Remove the worktree right after its branch is merged; run `git worktree prune` occasionally.

## License

MIT (to be confirmed before first release).