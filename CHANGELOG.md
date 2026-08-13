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

Phases landed on `dev` (milestone tags `v0.1.0-phaseN` … `v0.1.0-wave4-minor`):

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
- **Phase 27.8 (backend):** kanban columns = statuses — single axis (`columns.status`, migration 020, UNIQUE per board); DnD and status writes sync both ways; agent flow (claim/submit/review) moves the card. Frontend select reads project columns — pending (27.8.4).

### Changed

### Deprecated

### Removed

### Fixed

### Security

## [0.1.0] — TBD

Initial pre-alpha skeleton. No runtime code yet.