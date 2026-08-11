# AGENTS.md — Orenda Project Guidelines for AI Agents

> Краткий guide для AI-агентов, работающих с кодовой базой Orenda. Полные требования — в [[docs/PRD.md]], план — в [[docs/PLAN.md]].

## What is Orenda?

Local-first productivity suite (tasks, calendar, wiki) where **AI-agents are first-class citizens**. Single Go binary + React SPA, SQLite, port **2137**. Backup via git + sqlite snapshots.

## Stack

| Layer | Tech |
|-------|------|
| Backend | Go 1.22+ (chi, modernc.org/sqlite, jwt, gorilla/websocket, cobra) |
| Frontend | React 18 + TS + Vite + Tailwind + shadcn/ui |
| DB | SQLite (WAL mode) |
| Migrations | golang-migrate (sequential `NNN_*.up.sql` / `NNN_*.down.sql`) |

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
```

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

### When you start a phase
1. Read `docs/PLAN.md` for the phase definition.
2. Check Definition of Done.
3. Create worktree + branch `phase-X-Y-short-name` (см. «Worktree per task» — обязательно, без исключений).
4. Implement tasks in order.
5. Run `make test && make lint`.
6. Re-index the codebase knowledge graph after code changes: codebase-memory-mcp `index_repository` with `mode: "fast"` (`"full"` on first index). Code discovery runs through `search_graph`/`trace_path` — a stale graph misleads the next agent.
7. Open PR with checklist from Definition of Done.

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

## Key files to read first

- `docs/SESSION.md` — **session snapshot** (current state, decisions, next steps; read first when resuming work)
- `docs/PRD.md` — what we're building and why
- `docs/PLAN.md` — phases, tasks, criteria
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
# Start of task: branch + worktree off dev, placed NEXT TO the repo (never inside)
git worktree add ../orenda-<task> -b phase-X-Y-<name> dev

# ...work in ../orenda-<task>...

# Commit early, commit often: uncommitted work is unprotected —
# another agent's tree operation can silently destroy it.
git add -A && git commit -m "phase(X.Y): ..."

# After merge to dev:
git worktree remove ../orenda-<task> && git worktree prune
```

Rules:

- **The main checkout is read-only for you.** No edits there; no `git checkout` / `git reset` / `git clean` / `git restore` in any checkout you didn't create. Inspecting files read-only is fine.
- One branch = one worktree. A branch cannot be checked out in two places; create the task branch with `git worktree add -b`.
- Worktrees live **next to** the repo (`../orenda-<task>`), never inside it — a nested worktree breaks `go test ./...` and other tooling in the main tree.
- Gitignored content is not copied. In a fresh worktree run `npm install` in `web/` and `./bin/orenda migrate up` (each worktree gets its own `data/orenda.db`).
- Port 2137 is singleton: run a second instance with `ORENDA_SERVER__PORT=<other>` or don't run it at all.
- Merge to `dev` is a deliberate act: only after review, and never against someone's uncommitted work in the main tree.
- Remove the worktree right after its branch is merged; run `git worktree prune` occasionally.

## License

MIT (to be confirmed before first release).