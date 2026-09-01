# T118 + T119 Evidence

Smoke environment: worktree `task-118-kanban-fixes`, port 21421, seeded
project "Kanban Smoke". `before*` shots taken on the `origin/dev`
binary (pre-patch), `pw-*` shots on the patched build via the repo's
Playwright harness (real Chromium pointer drag).

## T118 — card reorder within a column

| File | Shows |
| --- | --- |
| `before-t118-board.png` | Pre-patch board, backlog column with 4 cards (T2 "First card" at the bottom). |
| `before-t118-afterdrag.png` | Pre-patch: after dragging "First card" to the top the order is UNCHANGED — same-column drop is ignored (`onDragEnd` early-returns). |
| `pw-before-reorder.png` | Patched build: fresh project, cards smoke A/B/C seeded via API (all `status=backlog`, see T119). |
| `pw-after-reorder.png` | Patched build: after pointer-dragging "smoke C" onto "smoke A", C sits above A; the UI POSTed `POST /tasks/{id}/move {"column_id":…,"position":-1024}` (reorder path). |
| `pw-after-reload.png` | Same board after F5 — order [C, A, B] persists (positions stored server-side). |

Unit coverage: `web/src/features/projects/cardPosition.test.ts` (9
cases on the pure positioning math) + offline-outbox position case in
`web/src/shared/offline/outbox.test.ts`. E2E suite `kanban.spec.ts`
4/4 green on the patched binary.

## T119 — backlog create status

| File | Shows |
| --- | --- |
| `before-t119-board.png` / `before-t119-modal.png` | Pre-patch: card "Repro T119 card" created in the BACKLOG column via the web client shows status **todo** (DB default wins because the web client sends only `{title, column_id}`). |
| `after-t119-modal.png` | Patched build: card created in the same backlog column now shows status **backlog** (handler syncs `column.Status` when the client omits `status`). |

Handler coverage: `internal/api/handlers_tasks_create_test.go` pins
all three paths — backlog create → `backlog`, explicit status
respected, inbox create (no column) unchanged.
