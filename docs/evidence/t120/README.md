# T120 Evidence — Task time estimate

Smoke environment: worktree `task-120-time-estimate` (patched build,
`8928835`+`b8d861a`), binary with embedded `web/dist`, backend on
**port 21431**, SQLite under `/tmp/t120-smoke`, user
`t120@smoke.local`, project «T120 Smoke», task T2 «Smoke estimate
task». The estimate column itself predates this task (001_init.sql:
117) — no migration involved; what's new is the editor + the 0-clears
PATCH contract.

| File | Shows |
| --- | --- |
| `before-editor-added.webp` | Pre-UI state: task opened with estimate **5400** seeded via API. Sidebar "Time tracking" shows the new owner-style line «оценка 1ч 30м · затрачено 0м» plus the EstimateEditor (min input, Set, clear) — before this branch the block only rendered spent minutes and Start timer. |
| `after-set-90min.webp` | After typing **90** in the editor and pressing Set: server row updated to `time_estimate_s = 5400` (verified via GET), sidebar still consistent («оценка 1ч 30м»). |
| `after-clear.webp` | After pressing **clear**: editor sent the `0` sentinel, server stores `NULL` (GET returns no field), sidebar degrades to «затрачено 0м», clear button disappears (nothing to clear), input placeholder returns to «Estimate (min)». |
| `card-timebadge-estimate.webp` | Board consistency: card T2 carries the TimeBadge «⏱ 0:00 / 1:30:00» (spent / estimate, H:MM:SS format) agreeing with the sidebar «оценка 1ч 30м». |

## Persistence

After a full page reload (goto → board → reopen task) the estimate
survived: sidebar «оценка 1ч 30м · затрачено 0м», badge unchanged —
value lives server-side (`tasks.time_estimate_s`), not in client
state.

## API smoke (same session, curl + session cookie)

| Step | Result |
| --- | --- |
| `PATCH {"time_estimate_s":5400}` | response `time_estimate_s: 5400` |
| `PATCH` without the field | estimate untouched (5400) |
| `PATCH {"time_estimate_s":0}` | estimate cleared (field absent from response, NULL in DB) |
| `PATCH {"time_estimate_s":5400}` (restore) | 5400 |

## Test coverage

- Handler: `internal/api/handlers_tasks_time_estimate_test.go`
  (create→201+value; PATCH set; PATCH 0→clear; PATCH without field
  →no-op) — `go test ./internal/api/ -run TestTaskTimeEstimate` ok.
- Vitest: `web/src/features/tasks/TaskViewBody.test.tsx`
  `EstimateEditor (T120)` — render (no clear when unset), set
  90 min→5400 s, clear→0 sentinel, negative input ignored. 14/14 in
  the file, 463/463 across `make test`.
