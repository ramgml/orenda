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

Phase-level work after v0.1.0 release. Currently empty.

## [0.1.0] — 2026-08-14

First release cut from `dev` (350+ commits past `main`).
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
- **Phase 27.8:** kanban columns = statuses — single axis (`columns.status`, migration 020, UNIQUE per board); DnD and status writes sync both ways; agent flow (claim/submit/review) moves the card. Frontend select reads project columns — pending (27.8.4).
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

### Changed
- **Phase 27.8:** kanban card status now authoritative — `task.status ≡ column.status`. Sidebar select reads project columns.
- **Phase 28.4:** JWT TTL 168h → 24h (forward-only — already-issued tokens honoured until exp); cookie `Secure` from config.
- **Phase 28.20:** Vite proxy targets followers `ORENDA_SERVER__PORT` env, no longer hardcoded `:2137`.

### Security
- **Phase 28.4:** `Secure` cookie attribute on JWT session + logout (loopback stays HTTP-friendly; HTTPS deploys opt in via `auth.cookie_secure: true`).
- **Phase 28.10:** `style-src 'self'` removed `'unsafe-inline'` to reduce CSS-exfiltration surface.
- **Phase 28.6:** opt-in pprof listener (loopback only by default); exposes heap/goroutine state — opt-in by design.

### Known gaps (not blockers, documented for next release)
- **Phase 7:** git client without `Status`/`TestConnection`; snapshot at 24h ticker (not cron 03:00).
- **Phase 8:** LWW conflict resolution is delivery-order, not `updated_at`-based (current handler comment is misleading).
- **Phase 10:** Email bot plain text (no HTML); VK Long Poll not implemented; weekly digest not implemented.
- **Phase 17:** UI density toggle for task cards (state in localStorage, no UI); no time estimate/spent badges.
- **Multi-user / multi-device sync:** next era.
- **Lint residue:** ≈95 issues remaining (Phase 28.15–28.17 closed 230 of 325).