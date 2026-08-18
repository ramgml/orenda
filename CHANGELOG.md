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

## [Unreleased]

### Changed
- **Phase 32.6:** Per-PR CI gates moved from GitHub Actions to local git hooks (wiki:ci-local-gates-hooks). `make hooks` sets `core.hooksPath = scripts/git-hooks` in the shared git config (all worktrees inherit it): `pre-commit` runs gofmt + `prettier --check` on staged files, `pre-push` runs `make lint-new` + `make test`. GitHub Actions now runs only the release gate (PR/push to `main`, tags `v*`), a test-only backstop on push to `dev`, and full pipeline on `workflow_dispatch`. PR-to-dev is intentionally silent. Bypass: `SKIP_ORENDA_HOOKS=1` (`--no-verify` forbidden). The superseded Phase 28.12 mechanism (`simple-git-hooks` + `lint-staged`) is removed from `web/package.json` — its `prepare` script resolved the relative `hooksPath` against `web/` and wrote dead hook files into `web/scripts/git-hooks/` on every `npm ci`.

### Fixed
- **Phase 32.6 follow-up:** the push-to-dev test backstop never fired — `test` has `needs: [lint]`, and GitHub propagates a skipped `needs` to dependent jobs whose `if` lacks a status function (pre-existing structure from Phase 30.1). The `test` job condition now starts with `always() && needs.lint.result != 'failure' && needs.lint.result != 'cancelled'`, so the backstop runs on push-to-dev while a failed/cancelled lint still blocks the release gate.

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