# Changelog

All notable changes to Orenda are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## Versioning policy

- **Branches:**
  - `main` — stable, tagged releases only. No direct commits.
  - `dev` — active development. All feature work lands here first.
  - `phase-X-Y-<name>` — feature branches off `dev` for individual tasks.
- **Tags:**
  - On `main`: `vX.Y.Z` — production releases.
  - On `dev`: `vX.Y.Z-phaseN` — phase milestones (e.g., `v0.1.0-phase1`).
- **Pre-1.0:** version is `0.MINOR.PATCH`. Anything may change between minors.
- **Source of truth:** `VERSION` file at repo root. `Makefile` reads it via `git describe`.

## [0.9.0] — 2026-08-27

Ninth pre-alpha release. Focus: MCP error transparency — HTTP status and response body from the Orenda server now reach the agent verbatim instead of collapsing into a generic JSON-RPC "tool error" line; plus documentation realignment for the CSP `style-src` policy.

### Changed
- **Task 73 (PR #94):** MCP tool errors now carry the server's own words. `readBody` (all `agentGet`/`agentPost`/`agentPut`/`agentPatch`/`agentDelete` helpers) turns a non-2xx response into `server returned 422 Unprocessable Entity: {"error":"invalid_project"}` — status line + body, truncated at 500 characters for oversized payloads. The JSON-RPC dispatcher (`handleToolsCall`) prefixes the tool name into the error `message` (`orenda_task_propose: server returned 404 Not Found: …`); previously the reason lived only in the `data` field while `message` was a static "tool error" string most clients render instead. 13 manual `orenda_*:` prefixes in tool validators removed — the dispatcher adds the name uniformly.

### Fixed
- **Task 76 (PR #91):** Docs realigned with reality: since Task 74 (PR #88) the production CSP allows inline styles (`style-src 'self' 'unsafe-inline'`) — the SPA legitimately injects `<style>` tags at runtime (the react-style-singleton scroll-lock chain pulled in by overlay/editor UIs, and BlockNote editor styles). Shipped Phase 28.10 entries above remain as-is: they document what that phase did at the time.

## [0.8.0] — 2026-08-27

Eighth pre-alpha release. Focus: the wiki gets a native block-based editor — BlockNote replaces the Tiptap rich-text path (T62/T63 cutover), backed by a per-page block tree in SQLite (migration `040`, T59), a server-side blocks→GFM projection (T60), a blocks service + REST API (T61), and front-end integration with `[[wiki:` autocomplete (T62); plus the CSP fix that allows runtime-injected inline styles (`style-src 'unsafe-inline'`, T74) and wiki-link projection into markdown `[[slug]]` references (T69).

### Added
- **Task 59 (PR #82):** Wiki pages can now store a structured block tree. Migration `040_wiki_blocks` adds the `wiki_blocks` table (flat rows ordered by `parent_block_id`/`position`, `ON DELETE CASCADE`) and a `content_format` column on `wiki_pages` (`markdown` | `blocks`). Domain gains `wiki.Block` with a type whitelist; `wiki.Repository` gains `GetBlocks`/`ReplaceBlocks` (transactional delete+insert). A review blocker fixed latent data corruption: an empty `content_format` on update now preserves the stored value via `CASE WHEN`.
- **Task 60 (PR #83):** Server-side `BlocksToMarkdown` projection — recursive renderer mapping every whitelisted BlockNote block type (paragraph, headings, bullet/numbered/check lists, quote, codeBlock, divider, GFM table, image, file) to GitHub-Flavored Markdown, including nested-list indentation and pipe escaping. Unknown block types are silently skipped instead of panicking. Two wire-format follow-ups: codeBlock content is parsed as an inline-item array (matching BlockNote 0.54 serialization), and children of non-list blocks render as siblings so no content is lost.
- **Task 61 (PR #84):** Blocks service + API: `GET/PUT /api/v1/pages/{slug}/blocks` get/replace the whole page block tree (recursive-count validation caps block count) and `POST /api/v1/pages/{slug}/attachments` uploads page attachments. OpenAPI specs updated in sync.
- **Task 62 (PR #85):** BlockNote 0.54 editor integrated into WikiPage: extended schema whitelists only approved block types for the slash menu; `[[` opens the WikiLinkMenu autocomplete near the cursor; dirty tracking, image upload, theme support, and dispatch of saves through the new `updatePageBlocks` API when the blocks editor is active. Legacy markdown pages keep editing through the same surface via markdown parse-in.
- **Task 74 (PR #88):** CSP `style-src` now allows inline styles (`style-src 'self' 'unsafe-inline'`). BlockNote injects component styles at runtime into `<style>` tags, which the previous `'self'`-only policy blocked, breaking editor rendering.

### Changed
- **Task 63 (PR #86):** Cutover — the Tiptap editor is removed; the BlockNote blocks path is the single editing surface for the wiki. Projection fixes as part of the cutover: the BlockNote 0.54 `tableCell` wrapper is handled correctly and `extractSlugs` now recognizes `link[href^="wiki:"]`.
- **Docs (PR #81):** Back-merge of `main` → `dev` after v0.7.0 — the 0.7.0 release-prep commits landed only on `main`; this folds them back so `dev` carries the full history again.

### Fixed
- **Task 62 (PR #85):** Review blockers in the new editor: `insertLink` deletes only the tracked `[[query` range (previously a document-wide search destroyed unrelated content on pages containing existing links); BlockNote's URI validation now accepts the `wiki:` protocol, so wiki links render with a real href instead of an empty one; dirty tracking no longer fires during initial load and works for block edits; Vite HMR compatibility restored by splitting pure functions out of `WikiLinkMenu.tsx`.
- **Task 69 (PR #87):** The markdown projection now converts `link[href^="wiki:"]` inline elements to `[[slug]]` references instead of leaving raw `[title](wiki:slug)` links, keeping wiki-link syntax consistent across the blocks↔markdown boundary.

## [0.7.0] — 2026-08-24

Seventh pre-alpha release. Focus: graceful-shutdown correctness (rate limiter + backup scheduler), test-suite health (hang fix, 50x speedup of `internal/api`), the status↔column invariant centralized in the service layer, mobile UI fixes (header overflow, Sign out in the drawer, ReviewRow overflow), and local-gate hardening (`tsc` in pre-push, deletion-only push skip).

### Added
- **Task 57 (PR #72):** Sign out is now available on mobile — a `Sign out` action added to the sidebar drawer, which is reachable at xs widths where the header button was hidden after the Task 56 overflow fix.
- **Task 44 (PR #67):** `tsc --noEmit` (`make web-typecheck`) is now part of the everyday local gate: it runs in the pre-push hook and in the CI backstop on push to `dev`, catching TS errors that would otherwise surface only in the release-gate build job.
- **Task 55 (PR #68):** Pre-push gates are skipped on deletion-only pushes (`git push origin --delete <branch>`) — no code crosses the wire, so there is nothing to gate. Mixed pushes (any code update alongside a deletion) still run the full gate.

### Changed
- **Task 46 (PR #69):** The status↔column invariant (`task.status ≡ column.status`, Phase 27.8) is now enforced centrally in the service layer instead of being replicated across handlers — writes that previously could silently desync card status and board column now go through one sync path with activity recording. Review follow-ups: Logger wiring, conditional sync, actorType attribution.
- **Docs (PR #73):** README split into an all-English `README.md` (main version) and a complete Russian translation `README.ru.md`, cross-linked at the top of both files.

### Fixed
- **Task 64 (PR #74):** `make test` no longer hangs — leaking `rateLimiter` `cleanupLoop` goroutines were blocking test process exit; the limiter now shuts down cleanly.
- **Task 66 (PR #75):** `internal/api` test suite speedup 431s → 8.7s (50x), restoring the cached fast-gate semantics of `make test` (Task 40).
- **Task 67 (PR #76):** `SetAPILogger(nil)` panicked on `atomic.Value.Store` — now guarded; the nil-observer flaky test fixed (parallel dropped, observer slice guarded).
- **Task 65 (PR #77):** Graceful shutdown now calls `deps.RateLimitClose` on server stop, closing the rate limiter instead of leaving it to die with the process.
- **Task 68 (PR #78):** Backup scheduler now receives the signal-aware context in `runServe` instead of `cmd.Context()`, so in-flight backup work observes SIGTERM/SIGINT during graceful shutdown.
- **Task 52 (PR #70):** ReviewRow overflow on long titles and assignee ids — review-queue cards stay within their column (before/after screenshots attached to the PR).
- **Task 56 (PR #71):** Header overflow at ≤375px — the health badge with a long dev version string (`ok · v0.6.0-12-g73fe483-dirty`) pushed `document.body.scrollWidth` to 400px; the badge is now width-constrained in the flex layout.

## [0.6.0] — 2026-08-22

Sixth pre-alpha release. Focus: human-readable ref formats across all surfaces (tasks T-refs, projects P-refs, wiki W-refs, courses C-refs, lessons L-refs), URL path-parameter escaping in MCP/CLI, and breaking cutover from legacy task ref forms.

### Added
- **Task 48 (PR #59):** T<N> ref format for tasks. Tasks now carry a T-prefixed human-readable reference (`T42`) in REST paths, CLI args, MCP `task_id` arguments, and a copyable `TaskNumberChip` in the UI. The legacy `#N` and bare `N` forms are retired (see Changed).
- **Task 49 (PR #61):** W<N> ref format for wiki pages. Wiki pages now carry a W-prefixed reference (`W7`) in REST paths, MCP `slug` arguments, and a copyable `WikiNumberChip` in the UI. Migration `037` adds the `number` column to `wiki_pages` and the `wiki_page_number_seq` sequence. Slugs matching the pattern `W<digits>` are rejected with 422 `slug_conflicts_with_w_ref`.
- **Task 50 (PR #60):** P<N> ref format for projects. Projects now carry a P-prefixed reference (`P3`) in REST paths, CLI args, MCP `project_id` arguments, and a copyable `ProjectNumberChip` in the UI. Migration `036` adds the `number` column to `projects` and the `project_number_seq` sequence.
- **Task 51 (PR #62):** C<N> and L<N> ref formats for courses and lessons. Courses carry a C-prefixed reference (`C5`), lessons carry a globally-numbered L-prefixed reference (`L12`) across all courses. Migrations `038`/`039` add the `number` column to `courses`/`course_lessons` and the `course_number_seq`/`lesson_number_seq` sequences. Copyable chips in the UI.

### Changed
- **Breaking (Task 48, PR #59):** Task ref format cutover — `#N` and bare `N` no longer resolve as task references. Use the T-prefixed form (`T42`) in REST paths, CLI args, MCP `task_id` arguments, and the UI chip. Legacy forms that don't match `T<N>` fall through to UUID lookup and return a generic `not_found` 404. Git-branch/commit/PR conventions remain bare numeric (`task-123-slug`, `task(123):`, `[Task 123]`).

### Fixed
- **Task 42 (PR #58):** MCP/CLI URL path-parameter escaping — all 14 URL-building sites now use `url.PathEscape` for path parameters, preventing silent response corruption when a ref value contains `#`, `/`, or other reserved URI characters.
- **Task 50 (PR #60):** `applyTaskPatch` error propagation — PATCH with a non-existent P-ref now correctly returns 404 instead of a silent 200 with no changes.

## [0.5.0] — 2026-08-22

Fifth pre-alpha release. Focus: shadcn/ui component migration (dialog, buttons, checkboxes, semantic tokens), LMS pace adaptation, agent task management (proposals/context/agent_notes), markdown rendering in task descriptions, build-system semantics swap (`make test` = cached fast gate, `make test-full` = CI backstop), Kanban fixes, and process documentation.

### Added
- **Task 11 (PR #39):** Task description view now renders Markdown. Descriptions written in markdown are rendered as formatted HTML in view mode, improving readability for long-form content.
- **Task 12 — shadcn/ui foundation (PR #42):** Added shadcn/ui base primitives (`Button`, `Badge`, `Card`, etc.) to `shared/ui` as the foundation for the component migration.
- **Task 12 — shadcn/ui dialog (PR #44):** Migrated feature modals to the shadcn `Dialog` primitive (`@radix-ui/react-dialog`), replacing self-made modals with proper focus trap, ESC handling, and ARIA semantics.
- **Task 12 — shadcn/ui slices (PR #45):** Migrated tasks/today/review slices to shadcn primitives, replacing custom components with the shared UI library.
- **Task 12 — shadcn/ui buttons (PR #46):** Migrated `ButtonsFeatures` slice to shadcn button primitives.
- **Task 12 — shadcn/ui checkboxes (PR #47):** Migrated raw HTML checkboxes to the shadcn checkbox primitive.
- **Task 12 — dark tokens (PR #48):** Replaced exact-1:1 `dark:` CSS classes with semantic tokens across the UI, enabling theme-consistent dark mode.
- **Task 15 (PR #38):** Agent API task management — agents can now edit proposed tasks, withdraw proposals, view task context, and write `agent_notes` (agent-only scratchpad field).
- **Task 26 (PR #33):** Web build now runs `npm ci` before `npm run build`, ensuring deterministic dependency installation for CI and release builds.
- **Task 27 (PR #35):** LMS pace adaptation — rolling velocity + drift calculation. Course pace notes now track a rolling velocity (lessons completed per day) and drift (deviation from target pace), enabling adaptive scheduling of study reminders.
- **Task 32 (PR #40):** Upload directory auto-creation on first attachment store. The uploads directory is created automatically when the first file attachment is stored, preventing errors on fresh installations.
- **Task 34 (PR #43):** Kanban agent label moved to bottom of task card, improving card readability by freeing the top area for title and status.
### Changed
- **Task 40 (PR #50):** Test semantics swap — `make test` now runs the cached fast gate (vitest), `make test-full` runs the uncached CI backstop. The former `test-gate` target was removed; `make test` serves as the local pre-push gate while `make test-full` is the CI/release backstop. Files touched: `Makefile`, git hooks, `ci.yml`, `AGENTS.md`, `README`, `ARCHITECTURE`, `RELEASE`.
- **Task 12 (PRs #42–#48):** Complete shadcn/ui migration across all major UI slices — modals → Dialog, buttons → shadcn Button, checkboxes → shadcn Checkbox, raw `dark:` classes → semantic tokens. The self-made component library is now replaced by the standard shadcn/ui primitives.
- **Task 37 (PR #53):** Dark palette normalization to shadcn tokens — `dark:` class count reduced from 242 to 115; remaining legitimate exceptions (e.g., border color overrides) documented in `wiki:frontend-shadcn-primitives`. Owner decision: variant B (normalize where token mapping exists, keep exceptions where it doesn't).

### Fixed
- **Task 28 (PR #36):** `test_scripts.sh` hermeticity — the test script now runs in isolation without depending on external state. `uninstall.sh` now rejects unknown flags with a usage message (previously `-purge` typo silently did nothing).
- **Task 29 (PR #37):** Flaky `TestHub_PublishDropsOnFullSubscriber` — removed scheduling dependency, eliminating CI flakiness.
- **Task 31 (PR #41):** Kanban cards overflowing right column boundary — fixed CSS containment so task cards stay within their assigned column.
- **Task 35 (PR #52):** Config tests isolated from `ORENDA_*` process environment — `clearORENDAEnv` helper sanitises the test env so config-reading tests don't inherit the running process's `ORENDA_*` variables, eliminating false positives.
- **Task 38 (PR #49):** Nil dereference guard in event create/update handlers — fixes potential panic on nil pointer when processing create/update events.
- **Task 39 (PR #51):** Synthetic occurrence ID resolution to master in GET/PATCH/DELETE handlers — fixes 500 on calendar event edit by correctly resolving synthetic occurrence IDs to their master events.

### Docs
- **PR #30:** `dev` branch workflow documented — `origin/dev` is the base for all feature branches; local `dev` is an ff-only mirror to prevent remote drift.
- **PR #31:** Review loop updated — PR review is assigned to the PM agent (omp, variant-B subagent dispatch) with a formal GitHub approve / request-changes verdict; merge to `dev` stays with the owner, who also closes the dogfood review-queue card as the merge audit. The PM additionally watches git-tree/worktree hygiene (drift, stale branches, stranded WIP). Updated `docs/DOGFOOD.md` steps 5–6 and `AGENTS.md` git workflow.
- **PR #32:** Owner merge protocol documented — merge to `dev` is the owner's responsibility after PM approval; PM monitors git-tree and worktree hygiene.
- **PR #34:** Added manual GitHub Release step to `docs/RELEASE.md` — the v0.4.0 release shipped without a GitHub Release (only a tag), now documented as a required step.

## [0.4.0] — 2026-08-19

Fourth pre-alpha release. Focus: human-readable task numbers (#N) on every surface, the dashboard chat backend MVP, harness-side PR sync, study-proposal server-side dedup, a UI-editable backup snapshot cron, the agent project surface with a full activity audit, the shadcn/ui foundation, and the Phase 30.16 lint-debt sweep. Also folds back two main-only commits that never reached `dev` (the 0.3.0 release prep and the Phase 32.2 capabilities feature, PR #5).

### Added
- **Task 14 (project ↔ wiki link):** `projects.wiki_slug` (migration `034`, FK `wiki_pages.slug ON DELETE SET NULL`) links each project to its wiki page. User `GET /api/v1/projects/{id}` returns it; `PATCH` (user and agent surfaces) accepts it — empty string unlinks, unknown slug → 422 `wiki_slug_not_found`. Each changed field writes its own activity row (`description_changed` / `wiki_slug_changed`). The Project settings tab gets a wiki-page section with a `<datalist>` autocomplete against the page tree.
- **Phase 32.13 (project activity + agent projects):** projects get the same audit treatment courses got in 0.3.0. Migration `024_project_activity` adds the `project_activity` table (newest-first feed per project). The agent namespace gains `GET /api/v1/agent/projects/{id}` and `PATCH /api/v1/agent/projects/{id}` (v1: `description`), each mutation writing an actor-attributed activity row with a before/after diff and publishing on the new `projects` WS topic. Wired end to end: `ProjectActivityRecorder` in the service layer, a narrow seam interface in `internal/api`, DI in `cmd/orenda/main.go`.
- **Phase 32.13 (shadcn/ui foundation):** the stack claimed shadcn/ui but nothing was installed — 6 self-made modals without focus trap, ESC handling, or aria dialog semantics. Phase 0 lands `web/components.json` (aliases → `@/shared/ui`, `@/shared/util/cn`), the Radix/CVA/clsx/tailwind-merge deps, shadcn CSS variables mapped to the existing `orenda-*` palette, the `cn()` helper, and the first primitive — `Dialog` on `@radix-ui/react-dialog` (already replacing the self-made modal in `Backups.tsx`).
- **Task numbers (#N):** every task now carries a human-readable sequential number alongside its UUID. A sequence watermark in storage assigns numbers monotonically; agent REST, the `orenda agent` CLI, and MCP tools all resolve `#N` references to the underlying task id; the web UI renders a `TaskNumberChip`. Naming conventions (branch `task-N-slug`, commit `task(N): ...`, PR `[Task N] ...`) are pinned in `AGENTS.md` / `docs/DOGFOOD.md`.
- **Phase 32.11:** Dashboard chat pane — commands-only MVP backend. Migration `032` adds the `chat_messages` table (sender_type user|agent, body_md, command, result_ref). `POST /api/v1/dashboard/chat` dispatches `/plan day` (synthesizes a study proposal via the existing Phase 31 pipeline — the result lands in the Dashboard proposals tray for accept/dismiss) and `/help`; plain text gets a canned reply (free-form LLM dialogue is a separate phase). `GET /api/v1/dashboard/chat/{thread}` replays history (cap 50). Both user and agent rows fan out live on the WS topic `chat`.
- **Phase 32.10:** `orenda agent pr-watch <task-id> [--repo owner/name] [--number N]` — harness-side PR sync helper. Local-first installs behind NAT can't receive GitHub webhooks, so the harness polls instead: the command reads the task context, extracts a PR number from the description (`PR #N` / `closes #N` / `refs #N` / `fixes #N` / bare `#N`), shells out to `gh pr view`, and prints the PR state as JSON. Orenda itself runs no daemon; the harness diffs the output and posts comments via `orenda agent comment`. 10 regex test cases pin the extraction.
- **Phase 32.7:** Backup snapshot schedule is now a real cron expression, editable from Settings → Backups and hot-reloaded without a restart. Replaces the fixed 24h ticker from Phase 7.5. A minimal UTC-only 5-field cron parser in `internal/backup` (~200 LoC, no new dependency) supports `*`, `n`, `n-m`, `*/k`, `n-m/k`, and comma lists with dom/dow OR semantics. `PUT /api/v1/backups/settings` accepts `snapshot_cron` + `snapshot_rotation_days`, validates via `backup.Parse`, persists to `backup_settings`, and merges into the live Service (DB > in-memory > `DefaultSchedule '0 3 * * *'`). Boot refuses to start on a bad YAML/env expression; a corrupted DB row falls back to the default and notifies via the FailureNotifier seam.

### Changed
- **Phase 33.3:** Agent-proposed tasks land in the backlog, not the review queue. `POST /api/v1/agent/tasks` now stamps `status=backlog, awaiting=none` — the owner triages on the kanban (drag to todo makes the task claimable), and the review queue is reserved for agent-submitted work (`status=review`). Owner decision 2026-08-18.
- **Phases 32.14–32.18 (lint-debt sweep, closes the bulk of Phase 30.16):** five batches against the pre-existing lint inventory — T1 zero-semantics, T2 test-file batch, T3 `unparam` in production code, T4 gocritic (`elseif`, `rangeValCopy`, `sprintfQuotedString`, `builtinShadow`, `httpNoBody`, `paramTypeCombine`, `unnamedResult`, `dupImport`, `equalFold`), T5 revive/staticcheck/contextcheck/nilerr/nilnil (stutter renames, deprecated `chi RealIP` dropped, request ctx passed to `runMaintenanceVerify`, deliberate swallow/no-row paths annotated with written reasons). `service/project` recorder types renamed to drop stutter. golangci-lint now excludes `web/node_modules`.
- **Phase 32.9:** Study proposals v2 — server-side dedup on `Propose`. Planners run twice a day; without dedup each run filed a fresh `pending` proposal and the Dashboard tray filled with duplicates. Dedup key = `(created_by_agent, course_id, normalized_title)` (trim + whitespace-collapse + lowercase via `study.NormalizeTitle`). On a hit the service returns the existing row with `Refreshed=true` (no create, no WS event). Resolved proposals (accepted/dismissed) are never dedup targets — the user's verdict is final.
- **Phase 32.8:** WAL archive → WAL checkpoint rename. The Phase 32.3 audit flagged `runWAL` / `wal_archive` as dead code; it wasn't dead — it runs `PRAGMA wal_checkpoint(TRUNCATE)` every 15 min to bound the WAL file. The name lied (no off-host WAL shipping exists). Kept the code, renamed everything (`runCheckpoint`, `wal_checkpoint` op/RecordLog) for accuracy; true PITR-style archive stays a separate decision (wiki:decision-log).
- **Phase 32.6:** File backlog frozen — `PLAN.md`/`SESSION.md` are archives; the live queue is the dogfood instance queue (the opencode watchdog now reads it instead of file rules; `.opencode/watchdog.json` rules emptied, checks/scripts left in place for re-enable from git history).

### Fixed
- **Phase 32.6:** Git hooks now unset `GIT_*` environment variables before spawning `go test` — a hook-spawned test run could corrupt the shared `.git/config`.
- Pre-existing test failures on `dev` fixed (`make test` was red); a compile break from the Phase 32.9 dedup refactor (`result.ID` → `result.Proposal.ID`) fixed.
- `orenda agent` CLI: auth-failure error messages now point at the config path (`~/.config/orenda/agent.yaml`) instead of leaving the agent to hunt for it.

### Docs
- `docs/RELEASE.md` — the release process (snapshot of wiki:release-process): branch/tag model, prep → PR → tag → dogfood-update steps, and the prohibitions (no release tags on `dev`, no merge to `main` without the release gate).
- `docs/DOGFOOD.md` — agent entry point now documents the one-time auth setup CLI chain and the token priority order (`~/.config/orenda/agent.yaml`), so a fresh agent session no longer hunts for URL/token.
- OpenAPI specs updated for the dashboard chat routes and the new project endpoints (route-coverage test unblocked).

## [0.3.0] — 2026-08-18

Third pre-alpha release. Focus: dogfood migration (the project is now managed inside its own running instance), agent self-service task creation, course activity audit, and local-first CI gates.

### Added
- **Phase 32.2:** `/api/v1/info` now advertises real capabilities. The response gained a `capabilities` object describing which optional features are actually wired in the running binary (previously the struct was zero-valued — every feature reported as off regardless of config). Lets operators and agents verify a dogfood/deployment update picked up the intended feature set.
- **Phase 32.5 (pilot #1):** `orenda backup push --with-snapshots` carries sqlite snapshots to the git remote. Previously the backup pipeline pushed only the markdown mirror — snapshots from `orenda backup snapshot` stayed local at `~/.local/share/orenda/snapshots/`, so a dead disk would lose every change not captured in a wiki markdown file (agent heartbeats, bot counters, time-tracking, FTS, sync_ops, raw notifications).
- **Phase 32.5 (course activity):** Course activity feed — audit who did what on a course. Migration `023_course_activity` adds the `course_activity` table + repository; the course service records actor-attributed events (user vs agent), and `GET /api/v1/courses/{id}/activity` serves the newest-first feed. Closes the Phase 29.5 deferral ("course-side activity-feed is not implemented").
- **Phase 33.1:** Agents can file new work themselves — `POST /api/v1/agent/tasks` (RequireAgent) creates a real task landing as `status=backlog, awaiting=human`, triaged through the existing review queue. Required fields `project_id`/`title`/`description_md` (400 without), 404 on unknown/foreign project, 401 without an agent token, user cookie rejected (namespaces split). Activity audit `task.created` with `actor_type=agent` + WS `task.created` on topic `tasks`. Surface parity: CLI `orenda agent propose` (`--project/--title/--description/--description-file/--priority/--blocked-by/--parent`) and MCP tool `orenda_task_propose` — one handler, three wrappers. Makes the DOGFOOD rule «новая работа = задача в инстансе» executable by agents.

### Changed
- **Phase 32.6:** Per-PR CI gates moved from GitHub Actions to local git hooks (wiki:ci-local-gates-hooks). `make hooks` sets `core.hooksPath = scripts/git-hooks` in the shared git config (all worktrees inherit it): `pre-commit` runs `gofmt -l` + `prettier --check` on staged files (<2 s), `pre-push` runs `make lint-new` + `make test` (~1 min). GitHub Actions now runs only the release gate (PR/push to `main`, tags `v*`), a test-only backstop on push to `dev`, and full pipeline on `workflow_dispatch`. PR-to-dev is intentionally silent. Bypass: `SKIP_ORENDA_HOOKS=1` (`--no-verify` forbidden). The superseded Phase 28.12 mechanism (`simple-git-hooks` + `lint-staged`) is removed from `web/package.json` — its `prepare` script resolved the relative `hooksPath` against `web/` and wrote dead hook files into `web/scripts/git-hooks/` on every `npm ci`.
- **Phase 33.1:** Kanban move backlog→todo now clears `awaiting=human` (narrow reset in `Service.Move`) so an agent-filed task becomes claimable on triage; the agent ready-list (`GET /agent/tasks?ready=true`) now includes unassigned todo tasks (`assignee_type IS NULL`) — previously the filter hid them and a triaged task could never appear.

### Fixed
- **Phase 32.6 follow-up:** the push-to-dev test backstop never fired — `test` has `needs: [lint]`, and GitHub propagates a skipped `needs` to dependent jobs whose `if` lacks a status function (pre-existing structure from Phase 30.1). The `test` job condition now starts with `always() && needs.lint.result != 'failure' && needs.lint.result != 'cancelled'`, so the backstop runs on push-to-dev while a failed/cancelled lint still blocks the release gate.

### Docs
- **Phase 32.4:** `docs/DOGFOOD.md` — the dogfood convention: project management (queue, постановки, review loop) lives in the running instance, not in PLAN.md; agent entry-point rules. `AGENTS.md` gained the local-gates (32.6) and worktree-per-task sections.
- **Phase 32.3:** Backup remote moved to SourceCraft + restore drill verified (ops runbook, no code change).

## [0.2.0] — 2026-08-17

Second pre-alpha release. Focus: agent surfaces (full wiki + course creation/activation over REST/MCP/CLI), a wide process-and-productivity sweep (CI, ops, kanban bulk edit, course curriculum CRUD), and study planning — a proposal tray integrated into the Today dashboard so a harness agent can fill the day with lessons and the operator accepts them one-tap.

### Added
- **Phase 29.1:** Agents can manage the wiki over REST. The wiki handlers mount verbatim under `RequireAgent` (they never read the user session; `wiki_pages` has no owner column): `GET /agent/pages` (tree), `GET/PUT/DELETE /agent/pages/{slug}` (PUT is the upsert), `PATCH /agent/pages/{slug}/move`, `GET /agent/pages/{slug}/backlinks`, plus `GET /agent/search` (FTS5). Same service as the user side, so the markdown mirror and WS events come for free. 4 integration tests pin the full CRUD round-trip, 401s for missing/cookie/bad credentials, FTS indexing of agent-written content, and unchanged user-side behaviour.
- **Phase 30.1:** GitHub Actions CI (`.github/workflows/ci.yml`) — `lint` → `test` → `build` → `e2e` with fail-fast, concurrency cancel-in-progress, and Go 1.26 / Node 24 / golangci-lint-action v6 caching. Lint scope: PR uses `--new-from-merge-base` (incremental gate on new code), push to `main` runs full (release-branch guard), push to `dev` skips lint (PR gate is the contract). Web lint: eslint + prettier --check. `.golangci.yml` cleanup: disabled pre-existing `hugeParam` gocritic rule (Phase 28.15 closed this for API handlers); renamed revive rule `error-returned` → `error-return` to match current schema.
- **Phase 30.2:** `sync_ops.Record()` failures are now observable. The 6 call sites in `POST /api/v1/sync` used `_ = syncOpsRecord(...)` — a failed write to the idempotency table was silently swallowed, the client could replay the op forever, and the operator saw nothing. The helper now increments `liveStats.syncOpsRecordFailures` and emits a `zap.Warn` with `client_id` / `server_id` / `err`. The `/api/v1/stats` endpoint exposes the counter as `sync_ops_record_failures`; OpenAPI schema updated. Tests in `internal/api/handlers_sync_test.go` (4 cases via `zaptest/observer` + mock `SyncOpsStore`).
- **Phase 30.3:** VK Long Poll transport. `internal/bot/vk.go` now runs a background goroutine that polls `groups.getLongPollServer` and `a_check` for inbound events when `bots[].type: vk` is configured with `token` and `group_id` (instead of — or alongside — the Callback API). Handles `failed=1` (server/key rotation) and dispatches `message_new` (type 4) events to the same `OnMessage` hook Telegram uses for Phase 21 inbox capture. The shared `captureToInbox` helper now serves both bots. Tests in `internal/bot/vk_longpoll_test.go` (10 cases against httptest VK / lp servers).
- **Phase 30.4:** Email is now sent as `multipart/alternative` with both text/plain and text/html parts. The HTML part carries inline-styled branding (Orenda bar, heading, body, optional link, action buttons row). Callback-style actions render as anchor buttons linking back to the review endpoint; URL-style actions render the pre-built link. `html.EscapeString` neutralises script-injection attempts via the title field. Email struct gained an optional `PublicBaseURL` for the action-button link generation. 13 tests in `internal/bot/email_html_test.go`.
- **Phase 30.5:** Weekly digest scheduler. The new `notifier.digest_interval` config (default 168h) drives a background ticker that, for every active owner, runs six aggregate queries (tasks done/created/awaiting/overdue, comments received, active timers) and pushes a `digest.weekly` event through the existing notifier pipeline. Rendered via `notifier.RenderWeeklyDigest` and templated through a new `WeeklyDigestFromEvent`. Self-comments excluded; awaiting/overdue are LIVE counts, not period-bounded. Disabled when `digest_interval <= 0`. 17 tests across `internal/service/notifier/digest_test.go` (7) and `cmd/orenda/digest_test.go` (10).
- **Phase 30.6:** `[[` autocomplete in the wiki editor. Typing `[[` opens a popup listing every page known to the editor (the same `/api/v1/pages` tree the sidebar uses, so agent-created pages from Phase 29 are picked up automatically). Picking a page inserts `[[slug]]` as plain text — the markdown mirror parses those on save and records `wiki_links` so backlinks work. Implemented with `@tiptap/suggestion` (new dep). Popup is a React component portaled into `document.body`, keyboard-navigable (↑/↓/Enter/Tab/Esc). 11 vitest in `WikiLinkSuggestion.test.ts` pin the filter logic and the tree-flatten helper.
- **Phase 30.7:** Backend now rejects `reject` reviews without a comment. Previously the front-end suggested a comment but the API accepted an empty string — agents could receive a return-to-fix with no actual reason. `TaskService.Review` now returns `ErrInvalidInput` (→ 400 via `writeError`) when `decision == reject && strings.TrimSpace(comment) == ""`. `approve` without a comment remains legal (silent ack). Whitespace-only comments are also rejected. Test in `claim_test.go::TestService_Review_RejectsWithoutComment` pins all three cases.
- **Phase 30.8:** Tasks with a `due_at` now appear on the calendar as all-day deadline markers. New endpoint `GET /api/v1/tasks/with-due?from=&to=` (backed by `task.Repository.ListByDueBetween`) returns tasks whose due_at falls in the requested window. The calendar's `CalendarPage` consumes the endpoint and renders each task as a 📌-prefixed all-day event; done tasks render with a ✓ suffix. OpenAPI schema updated (both files in sync). Test in `task_repo_test.go::TestTaskRepo_ListByDueBetween_Calendar` pins in-range, out-of-range, no-due, and ordering.
- **Phase 30.9:** Backup `Status` endpoint + UI status line. `GET /api/v1/backups/status` returns a read-only snapshot (count + latest path/size + timestamp) so the operator can confirm backup state without triggering a push. `POST /api/v1/backups/test` (Phase 28.10) remains the connection-check path. Settings → Backups now shows the count and latest snapshot timestamp above the snapshot list. Cron schedule parsing remains deferred (would require a cron-parser dependency; `SQLiteSnapshotCron` field exists but is read-only). OpenAPI schema updated (both files in sync).
- **Phase 30.10:** Quick capture accepts an optional due date. The modal gained a `<input type="date">` field (off the hotkey path — `q` → type → Cmd/Ctrl+Enter still captures without touching it); the picked date is anchored to local midnight and sent as RFC3339 `due_at`. Backend fix: `POST /api/v1/inbox/tasks` previously dropped `due_at` silently (only the project create path mapped it) — the inbox handler now shares the same parsing contract. Tests: 1 Go API round-trip, 4 vitest (payload shape with/without date, field reset), 1 E2E (due persists server-side, TZ-robust instant compare).
- **Phase 30.11:** WIP-limit feedback on the kanban. When a drag is rejected by the server (422 `wip_limit`), the board surfaces a specific toast: `Column "<name>" is at WIP limit (N of M). Pick another column or finish a task first.` — using the live count from local state so the operator knows which limit they hit. Columns at the limit get an amber ring + border so the bottleneck is visible without opening the column header. Backend unchanged (Phase 23.1 already returned the right error code).
- **Phase 30.12:** Time badges on the kanban TaskCard. New `TimeBadge` component renders ⏱ spent/estimate in H:MM:SS (red border on over-budget) and a pulsing ● marker when a single-active-timer is open. Backend unchanged — `time_estimate_s`/`time_spent_s` and the `started_at`/`completed_at` pair already shipped (Phases 17 and 4). 7 vitest cases pin all three states plus edge cases (negative seconds, hours overflow).
- **Phase 30.15:** Ops-script hygiene. `scripts/uninstall.sh` now has `--help` (prints usage, exits 0) and rejects unknown flags with exit 2 — previously `-purge` (typo) silently did nothing. `scripts/update-dogfood.sh` adds `--help`, `--force` (logged as a warning but doesn't bypass the main+clean check), and `--remote <name>` (configurable remote, default `origin`). `scripts/test_scripts.sh` (NEW): 7 smoke tests cover both scripts' flag parsing.
- **Phase 30.16:** Lint sweep — first pass closed ~8 pre-existing issues. `internal/bot/bot.go` lost its unused `var now = time.Now` test seam; `cmd/orenda/backup.go` lost the pre-Phase-22 `runBackupRestore` (replaced by `runBackupRestoreWithVerify`); `telegram_inbox_test.go` lost the no-op `seedSubscription` stub; `dependencies_endpoint_test.go` lost the `depFixtures` placeholder; `review_queue_test.go` lost its `reviewQueueFixture`; `agent.go` lost `agentPut` and `agentDelete` (unused transport helpers); `event.go` lost the `actorID` parameter from `publish`; `notify_test.go` renamed `cookie` → `_` in `seedProjectAndTask`. ≈85 issues remain — mostly unused test fixtures and stylistic gocritic warnings; will close opportunistically. CI gate (Phase 30.1) only blocks new-code lint, not pre-existing.
- **Phase 30.17:** Bug fix — `writeError` now maps `taskservice.ErrInvalidInput` to 400 `invalid_input`. Previously the switch only listed domain-package sentinels (`task.ErrInvalidInput`, etc.), so a `POST /api/v1/tasks/{id}/review {decision:"reject", comment:""}` (Phase 30.7's validation) returned 500 `internal` instead of 400. Same hole affected bogus `decision` values. New test `TestP3_ReviewWithoutCommentReturnsBadRequest` pins all three failure modes (reject without comment, whitespace-only comment, bogus decision). The test would have caught the gap at Phase 30.7 acceptance — the missing API test there is why this slipped through.
- **Phase 29.2:** `orenda agent` CLI gains `pages list|get|put|delete|move|backlinks` and `search` (with `--json` parity and stdin support for `pages put`). Also closed a latent transport bug: `doRaw` was assigning paths with query strings into `u.Path`, percent-encoding `?` — `?ready=true` on `next` silently failed. 4 cobra-level tests pin verb/body/encoding.
- **Phase 29.3:** MCP tools for the new wiki/course surfaces — `orenda_pages_list`, `orenda_pages_get`, `orenda_pages_save`, `orenda_pages_delete`, `orenda_pages_move`, `orenda_search`. Also fixed a latent bug: `orenda_await` was posting to the user-side `/api/v1/events/await` (401 on opaque agent tokens) — now hits the agent namespace. 8 tests pin the new tool set and the verb/body/encoding.
- **Phase 29.4:** `POST /api/v1/agent/courses` — agents can create courses. Owner is `FirstNonSystem` (single-user today, but the column existed for multi-user). `SkipGenerator` is forced (the agent is the generator — spawning a generator task would shadow itself). No owner → 503 `owner_not_configured`; missing title → 400.
- **Phase 29.5:** `POST /api/v1/agent/courses/{id}/activate` — completion of the end-to-end course-creation loop. Shared `approveCourseCore` extracted; both user-side `approve` and agent-side `activate` go through the same service path. Missing course now returns 404 on both surfaces (was 500). Course-side activity-feed is not implemented (the audit log exists only for tasks); flagged as deferred.
- **Phase 29.6:** SKILL.md gets a new §4.4 "Build me a course on X" — end-to-end scenario for the agent harness (create → curriculum → materialize → activate), with curl examples matching the style of the delegation-loop section. §2.2 documents the new CLI subcommands; §6.1's reference table lists the new endpoints. OpenAPI schema updated (both files in sync).
- **Phase 29.7:** End-to-end smoke verified against a real binary on a tmp DB at port 21431 (skill install path for the harness is wired): tutor token creates a course with curriculum (1 module + 2 lessons + 1 exact quiz), materialises both lessons, activates. User-side sees the course as `active` with the first lesson `open`. Wiki CRUD via agent token (create / move / backlinks / search / edit-upsert / delete with cascade).
- **Phase 30.13:** Granular CRUD for course curriculum with stable IDs. New endpoints (user + agent mirrors, same handlers): `POST /courses/{id}/modules`, `PUT /courses/{id}/structure` (IDs-only reorder with exact-coverage validation in tx), `PATCH/DELETE /modules/{id}`, `POST /modules/{id}/lessons`, `PATCH/DELETE /lessons/{id}`, `PATCH/DELETE /quizzes/{qid}`. Done/archived courses are frozen (422 `invalid_transition`). No rows are recreated, so lesson progress and task references survive the edit by construction. The `CourseCurriculumEditor` runs in active mode: `diffCurriculum` + `applyGranularPlan` apply updates / creates / deletes / structure changes with a temp-id → server-id map for new elements; DnD reorder via the same dnd-kit primitives the kanban uses (cross-module); markdown-import parses `##` modules / `###` lessons / `- [exact]` / `- [open]` quiz lines. Lesson content goes through the existing `updateLessonContent` (extended `lessonContentUpdates` op). OpenAPI schema updated (both files in sync), `TestOpenAPI_RouteCoverage_FullRouter` passes; mutation-check confirms the order-detection logic is load-bearing.
- **Phase 30.14:** Column CRUD accepts and validates a machine key (slug fallback + board-local dedup); renaming a status column fans out `task.status`, activity, and `task.updated` WS. `Status.IsValid` accepts custom project keys. New `POST /api/v1/tasks/bulk-edit` endpoint with the same PATCH side-effects (done / completed_at, awaiting, activity, mirror), per-task results/errors, and WS updates. Kanban got selection checkboxes + a bulk status/priority/assignee bar; Add/Edit column UI got a machine-key field. OpenAPI specs in sync.
- **Phase 31.1:** Study-planning migration `022_study_planning.{sql,down.sql}` — `courses.pace_notes_md TEXT NOT NULL DEFAULT ''`; `tasks.study_course_id TEXT NULL REFERENCES courses(id) ON DELETE SET NULL` + partial index; `study_proposals(id, course_id, title, body_md, target_date, status, created_by_agent, accepted_task_id, created_at, resolved_at)` with CHECK constraint on status and indexes for the pending queue. Test pins up-форма, idempotency, and down-recovery.
- **Phase 31.2:** Domain types — `course.Course.PaceNotesMD` (with 64 KiB cap in `Validate`), `task.Task.StudyCourseID`, and a new `internal/domain/study` package (`Proposal` with `Status` enum, sentinels for `ErrNotFound`/`ErrInvalidInput`/`ErrTransition`, `AcceptAllowed`/`DismissAllowed` lifecycle, `Validate` for trim/sizes/date format). 6 sub-tests on `Validate` + lifecycle matrix + 5 `Course.Validate` cases + 3 `Task.Validate` cases.
- **Phase 31.3:** Storage — `study_proposal_repo` (Create/ListPending/Get/MarkAccepted/MarkDismissed, both updates idempotent via conditional WHERE + existence check); `task.StudyCourseID` round-trip in Create/GetByID/Update/ListAwaitingReview with FK `SET NULL` on course delete; `course.PaceNotesMD` round-trip in Create/Get/List/Update plus a focused `UpdatePaceNotesMD` PATCH. `docs/DB.md` updated with the new section. Tests cover full lifecycle (6 sub-tests), task round-trip (5), course round-trip (6).
- **Phase 31.4:** `internal/service/study` — `Propose` (agent creates proposal), `Accept` (idempotent: re-accept returns the previously-created task, no duplicate), `Dismiss`. Materialisation reuses `task.Repository.Create` and a narrow `ActivityRecorder`; `due_at = max(target_date, today)` end-of-day UTC. Lifecycle guards: accept on dismissed → `ErrTransition`; concurrent accepts serialize on conditional UPDATE. WS emits to topic `tasks` as `study.proposed`/`study.accepted`/`study.dismissed`. 10 tests cover happy paths, lifecycle guards, idempotency, and activity audit.
- **Phase 31.5:** Agent REST — `POST /agent/study-proposals` (CreatedByAgent = `Identity.ActorID`, 201 on success, 400/401/503), `GET /agent/courses?status=active` enriched with `progress` (lessons_total/lessons_done + open_lessons array with module title) + `pace_notes_md` + `pace`, `PATCH /agent/courses/{id}` for narrow `pace_notes_md` edits (trim + cap applied via `UpdatePaceNotesMD`). 9 tests cover happy paths, missing fields, identity, service-not-wired, oversized input, and enrichment.
- **Phase 31.6:** User REST — `GET /api/v1/study-proposals` (pending only, by created_at), `POST /api/v1/study-proposals/{id}/accept` (201 on first, 200 on idempotent re-accept with the same task), `POST /api/v1/study-proposals/{id}/dismiss` (200). `accept`/`dismiss` on a resolved proposal → 409 `proposal_resolved`; missing or wrong-owner proposal → 404.
- **Phase 31.7:** Today — `todayResponse.proposals` carries the pending list; `due_today` includes open study reminders with `due_at <= today`; `overdue` excludes them. Read-only filter — no cron, no sweep, no scheduled mutation. Missed-day reminders never turn red; non-study tasks are unaffected. Tests pin all three behaviours.
- **Phase 31.8:** MCP + CLI parity — `orenda_courses_list` (status filter, progress, pace_notes) and `orenda_study_propose` (course_id?, title, body_md?, target_date). CLI: `orenda agent courses list --status active` and `orenda agent study-propose --json`. Tests pin verb/body/encoding per the `orenda_tools_test.go` convention.
- **Phase 31.9:** Frontend — TodayPage gains a "Предложено" tray card (title, body preview, link to course, target_date, Accept/Dismiss buttons; invalidates via the existing `tasks` topic). Study reminders in `due_today` get a 📖 marker with a course link. The course detail page displays + edits `pace_notes`. Vitest pins the tray flow, the invalidation, the marker, and the pace-notes editor.
- **Phase 31.10:** OpenAPI specs (both `internal/api/openapi.yaml` and `docs/openapi.yaml` in sync) — new paths (`/agent/study-proposals`, `/study-proposals*`, `PATCH /agent/courses/{id}`), new schemas (Proposal, enriched course payload, `todayResponse.proposals`). `TestOpenAPI_RouteCoverage_FullRouter` passes. SKILL.md gets a new "Plan my day" section documenting the harness-agent loop (`orenda_courses_list` → propose N → user accepts in Dashboard) with curl examples.
- **Phase 31.11:** End-to-end smoke verified against a real binary on a tmp DB. Tutor token creates a course with `pace_notes` → curriculum (module + 2 lessons) → materialises both → activates. Planner token: `GET /agent/courses?status=active` returns progress + pace_notes → 2 proposals. User cookie: `GET /today` shows the 2-entry tray; accept the first → `due_today` carries the resulting task with `study_course_id`; re-accept → same task (idempotent); dismiss the second → tray empty. SQLite `UPDATE tasks SET due_at = yesterday` simulates a missed day: the reminder still appears in `due_today`, never in `overdue`. Smoke output: `SMOKE OK`.

### Changed
- **Phase 30.13:** Course curriculum is now safely editable in active courses. The previous contract (`SubmitCurriculum` atomic swap) was destructive in `active` — it recreated rows and reset lesson progress. New granular CRUD keeps stable IDs across edits, so lesson progress and task references survive by construction. The editor exposes a dnd-kit reorder (cross-module) and a markdown-import path. Lesson content edits use the same `updateLessonContent` endpoint.
- **Phase 30.14:** Kanban columns gained an explicit machine key (slug fallback + board-local dedup). Renaming a column fan-outs the new `status` to all tasks in that column, with activity and WS `task.updated` events. `Status.IsValid` now accepts custom project keys. Kanban got multi-select bulk-edit (status / priority / assignee) using the same PATCH side-effects as the sidebar.
- **Phase 31.7:** Today page now carries a `proposals` field (pending study proposals) and routes open study reminders into `due_today` instead of `overdue`. Missed reminders never turn red — a missed day isn't a deadline breach; the reminder sits in `due_today` until the user clears it.

### Fixed
- **Phase 29.2:** CLI transport was percent-encoding `?` in query strings (latent bug — `?ready=true` on `next` silently failed). `doRaw` now uses `url.URL{RawPath: …}` / `url.Values.Encode()` so query parameters survive.
- **Phase 29.3:** `orenda_await` MCP tool was posting to `/api/v1/events/await` (user side) — every agent invoke got 401. Now hits `/api/v1/agent/events/await`.
- **Phase 29.5:** `POST /api/v1/courses/{id}/approve` (and the new agent-side `activate`) returned 500 on a missing course — `coursesvc.ErrNotFound` wasn't mapped. Both surfaces now return 404.
- **Phase 30.7 / 30.17:** `reject` decisions without a comment returned 500 `internal` instead of 400 — `writeError` didn't map `taskservice.ErrInvalidInput`. Now 400 `invalid_input`; whitespace-only comments also rejected.

### Security
- **Phase 30.5:** Weekly digest SMTP traffic — no auth/credentials changes. (No new security entry.)

### Docs
- **Phase 31.10:** SKILL.md grew a "Plan my day" section — the harness-agent workflow for proposing study reminders via MCP, with curl examples.

### Known gaps (not blockers, documented for next release)
- **Phase 7:** git client without `Status`/`TestConnection`; snapshot at 24h ticker (not cron 03:00).
- **Phase 17:** ~~no time estimate/spent badges on task cards~~ — closed in **Phase 30.12**.
- **Phase 30.1 / 30.16:** CI lint gate is incremental (`--new-from-merge-base`); ~95 pre-existing lint issues remain in dev (gocritic dupImport/unnamedResult/paramTypeCombine, unparam, nilnil, unused, contextcheck, etc.). Tracked as Phase 30.16 — close opportunistically when touching the affected files.
- **Phase 32:** Dogfood migration — project management moves from PLAN.md/SESSION.md into the running instance (Phase 32.1 ships this release; 32.4/32.5/32.6 follow).
- **Multi-user / multi-device sync:** next era.

## [0.1.0] — 2026-08-16

First release cut from `dev` (367 commits past `main`).
This is `pre-alpha` — single-user, single-binary, local-first. No
migration guarantees between minors yet.

### Added
- **Phases 0–10 (core):** projects/tasks/kanban with DnD + WS, agents with Bearer tokens (claim/release/submit/review), comments + mentions + attachments, activity log, calendar (RRULE) + time tracking, wiki + FTS5 search, notifications inbox + bots (Console/VK/Telegram/Email/Webhook), git-mirror + sqlite-snapshot backups, PWA offline outbox + sync, security headers + rate limit, install.sh/systemd.
- **Phases 11–17:** project tabs UI, kanban column CRUD/reorder + WIP limits, tags, subtasks → child tasks, task dependencies + agent ready-listing, inbox-as-no-project, rich task cards.
- **Phase 18 + 27.4:** LMS courses (courses/modules/lessons/quizzes) with AI-tutor loop — generator task, atomic curriculum swap, lesson materialization, quiz answers (exact auto-check; open → tutor review task), LessonPage.
- **Phases 19–21:** review queue + sidebar badge, Today dashboard, quick capture + Telegram auto-capture.
- **Phase 22 + 22.3:** backup restore (CLI pipeline + safety copy + integrity checks), maintenance mode, restore via UI, Telegram /start onboarding.
- **Phases 24–25:** OpenAPI spec served from the binary + route-coverage test, stats/slow-log; `orenda agent` CLI + SKILL + MCP stdio proxy.
- **Phase 26 + 27.1–27.3:** vitest in `make test`, Playwright E2E (`make test-e2e`), SPA embedded via `//go:embed`, WS cookie auth, tags in list payload.
- **Phase 27.5:** `.down.sql` for every migration + `migrate down` runner with irreversible markers.
- **Phase 27.6:** owner-side course editing — user curriculum swap (carries quizzes), quiz append in both namespaces, lesson content edit, generator-task retired on manual submit, `skip_generator`.
- **Phase 27.7:** task card sidebar edits Status/Priority/Assignee; manual `done` stamps `completed_at`; awaiting normalized on direct status writes.
- **Phase 27.8:** kanban columns = statuses — single axis (`columns.status`, migration 020, UNIQUE per board); DnD and status writes sync both ways; agent flow (claim/submit/review) moves the card. Frontend select reads project columns (27.8.4).
- **Phase 27.9:** WS multi-topic fan-out (`ws.AllTopics` + `subscribeAll`); report titles via `TaskTitleLookup`; course-task WS/activity via `courseTaskActivityRecorder`.
- **Phase 27.10:** column color initialized on edit/reopen; `patchColumnHandler` publishes `column.updated`.
- **Phase 27.11:** agent-namespace comment + await endpoints (`/api/v1/agent/tasks/{id}/comments`, `/api/v1/agent/events/await`); full-router OpenAPI route coverage.
- **Phase 28.1:** `PUT /api/v1/backups/settings` 200 with restart-to-apply banner; restart-to-apply → hot-reload (28.9).
- **Phase 28.2:** Settings hub page (`/settings`) with 4 cards + About block from `/api/v1/stats`.
- **Phase 28.3:** TaskModal scroll fix (backdrop всегда items-start, body scroll-lock via `useBodyScrollLock`).
- **Phase 28.4:** security defaults — JWT TTL 24h, cookie Secure from `auth.cookie_secure`, logout matches.
- **Phase 28.5:** `task.commented` / `task.attachment_added` emit via `ActivityRecorder`; `Bot.Stop()` called on shutdown (best-effort loop).
- **Phase 28.6:** opt-in pprof listener (`DebugPProf` + `PProfAddr`, loopback); govulncheck Makefile target.
- **Phase 28.7:** Prettier 3.x config + `npm run format` / `format:check`.
- **Phase 28.8:** rate_limit YAML section (`auth`/`anon` buckets + per-sec).
- **Phase 28.9:** hot-reload backup settings (`atomic.Pointer[Config]`).
- **Phase 28.10:** CSP-tightened — `style-src 'self'` (no `'unsafe-inline'`).
- **Phase 28.11:** `docs/ARCHITECTURE.md` (556 lines, 13 sections).
- **Phase 28.12:** pre-commit Prettier hook via `simple-git-hooks` + `lint-staged`.
- **Phase 28.14:** README updated to post-Phase-26 state.
- **Phase 28.15:** hugeParam cleanup — handler factories take `*Dependencies` by pointer.
- **Phase 28.16:** errcheck cleanup — re-enable default `exclude-use-default: true`.
- **Phase 28.17:** small-cluster lint cleanup (unparam, unused, prealloc, ineffassign, nilnil).
- **Phase 28.18:** `docs/PLAN.md` + `SESSION.md` sync reflecting 28.7–28.17.
- **Phase 28.19:** `agents.type` as free-form label set (JSON-array in TEXT column, migration 021); chips-input UI, OR filter `?type=a&type=b`, AssigneeChip joins agent name.
- **Phase 28.20:** dev/dogfood separation — dev on `:2138`, usage on `:2137`, e2e on `:21371`; `install.sh` channel guard (refuse non-main + dirty, `--force` override); `update-dogfood.sh` one-command refresh; vite proxy follows `ORENDA_SERVER__PORT`; startup log carries `db_path`.
- **Phase 10 (test-send UI):** `POST /api/v1/bots/test` one-off message through any configured bot (whitelist webhook/email/telegram/vk; `console` excluded); UI dropdown + submit; per-bot target pre-check.
- **Phase 15 (close-out):** 409 `lock_taken` returns `holder_agent_id`/`holder_agent_name`/`claimed_at`; `/tasks/:id/context` and `/agent/tasks/{id}/context` carry `blocked_by` (open dependency ids) + `lock_holder`; `?ready=true` excludes self-assigned tasks.
- **Phase 28.21:** tracked `configs/config.example.yaml` (install no longer crashes on fresh clones); `install.sh` generates a random JWT secret into `$DATA_DIR/env` (mode 600) — no repo-public placeholder in the systemd unit.
- **Phase 28.23:** card density toggle UI on the kanban toolbar (closes the Phase 17 debt — the `orenda.kanban.cardDensity` flag finally has a writer); shared UI primitives (`Loading`/`ErrorBanner`/`EmptyState`); WS→Query invalidation for the `agents` topic; `AuthContext` unit tests.

### Changed
- **Phase 27.8:** kanban card status now authoritative — `task.status ≡ column.status`. Sidebar select reads project columns.
- **Phase 28.4:** JWT TTL 168h → 24h (forward-only — already-issued tokens honoured until exp); cookie `Secure` from config.
- **Phase 28.20:** Vite proxy targets followers `ORENDA_SERVER__PORT` env, no longer hardcoded `:2137`.
- **Phase 28.23:** `zustand` and `@tiptap/extension-bubble-menu` dropped from deps (unused); `idb` moved to runtime dependencies.

### Fixed
- **Phase 28.21:** `/api/v1/auth/login` was on the rate-limiter skip list — anonymous password guessing was unlimited; it now draws from the per-IP bucket.
- **Phase 28.22:** N+1 in `GET /api/v1/agent/tasks` (batch `BlockersForTasks`); `/today` enrichment no longer scans the whole tasks table (`Filter.IDs`); dead code sweep (single `go vet` finding closed).
- **Phase 28.23:** WS re-subscribe race in `useWebSocketTopic` — inline-arrow handlers caused unsubscribe+resubscribe on every render, events in the gap were lost; handler now lives in a ref, subscription keyed by topic only (mutation-checked).

### Security
- **Phase 28.4:** `Secure` cookie attribute on JWT session + logout (loopback stays HTTP-friendly; HTTPS deploys opt in via `auth.cookie_secure: true`).
- **Phase 28.10:** `style-src 'self'` removed `'unsafe-inline'` to reduce CSS-exfiltration surface.
- **Phase 28.6:** opt-in pprof listener (loopback only by default); exposes heap/goroutine state — opt-in by design.
- **Phase 28.21:** systemd unit no longer ships a repo-public placeholder JWT secret; login endpoint is rate-limited (see Fixed).

### Docs
- **Phase 28.21:** Phase 8 sync conflict resolution documented as delivery-order LWW (correct for single-device outbox; timestamp-based LWW deferred to the multi-device era).

### Known gaps (not blockers, documented for next release)
- **Phase 7:** git client without `Status`/`TestConnection`; snapshot at 24h ticker (not cron 03:00).
- **Phase 10:** Email bot plain text (no HTML); VK Long Poll not implemented; weekly digest not implemented.
- **Phase 17:** no time estimate/spent badges on task cards.
- **Phase 30.1/30.16:** CI lint gate is incremental (`--new-from-merge-base`); 73 pre-existing lint issues remain in dev (gocritic dupImport/unnamedResult/paramTypeCombine, unparam, nilnil, unused, contextcheck, etc.). Tracked as Phase 30.16 — close opportunistically when touching the affected files.
- **Multi-user / multi-device sync:** next era.
- **Lint residue:** ≈95 issues remaining (Phase 28.15–28.17 closed 230 of 325).