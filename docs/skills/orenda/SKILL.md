---
name: orenda
description: Work with the Orenda productivity suite (tasks, kanban, wiki, courses) via REST API, `orenda agent` CLI, or MCP tools. Use when the user mentions Orenda, orenda tasks, claiming/submitting tasks, agent delegation loop, review queue, inbox, or importing notes into Orenda.
---

# Orenda — Agent Skill

> **Audience:** AI agents that work with Orenda via its REST API or the `orenda agent` CLI.
> **Goal:** reliably run the agent-side half of the delegation loop.
> **Read this once.** The API is small; the etiquette is the load-bearing part.

Orenda is a personal productivity suite (tasks, calendar, wiki) where
you — the agent — are a first-class participant. The user creates
tasks, you claim them, you do work, you submit for review. The
delegation cycle is symmetric: human → agent (claim), agent → human
(submit → review).

This document describes the workflow, the HTTP surface, and the
shared `orenda agent` CLI. It is the authoritative source for the
agent etiquette; the API reference is `/docs/API.md` and the REST
endpoints speak for themselves.

---

## 1. Three surfaces, one workflow

You integrate with Orenda through any of three surfaces:

| Surface | When to use |
|---|---|
| **REST API** (`/api/v1/agent/*`) | Direct HTTP from any tool. Most flexible. |
| **`orenda agent` CLI** | Shell scripts, lightweight agents, MCP stdio bridges. |
| **MCP server** (`orenda mcp-proxy`, Phase 25) | Native tool-discovery for MCP clients. |

Pick one. The CLI is a thin wrapper over the REST API — every
subcommand has the same wire shape as the HTTP endpoint. The
examples below use the CLI because it shows the workflow shape
without boilerplate.

---

## 2. Pick a surface

### 2.1 REST API

```bash
BASE=http://localhost:2137
TOKEN=orenda-agent-...

# Confirm the token works.
curl -H "Authorization: Bearer $TOKEN" $BASE/api/v1/agent/me

# List ready tasks.
curl -H "Authorization: Bearer $TOKEN" \
  "$BASE/api/v1/agent/tasks?ready=true&limit=10"
```

### 2.2 `orenda agent` CLI

```bash
# Configure once via env (preferred) or ~/.config/orenda/agent.yaml.
export ORENDA_URL=http://localhost:2137
export ORENDA_AGENT_TOKEN=orenda-agent-...

# Or via the config file:
cat > ~/.config/orenda/agent.yaml <<EOF
url: http://localhost:2137
token: orenda-agent-...
EOF
```

Then drive the workflow with subcommands:

```bash
orenda agent me           # confirm the token works
orenda agent projects list  # list projects (source of --project for propose)
orenda agent next          # await + claim a task in one shot
orenda agent propose --project <id> --title "..." --description-file task.md
                           # file NEW work (lands in the human's review queue)
orenda agent claim <id>    # claim a specific task by id
orenda agent context <id>  # read the full task snapshot
orenda agent comment <id> "...markdown..."   # leave a comment as the agent
orenda agent submit <id>   # mark ready for human review
orenda agent release <id>  # give up a claim
orenda agent await         # long-poll for the next event

# Wiki + search (Phase 29.2):
orenda agent pages list                    # wiki page tree
orenda agent pages get <slug>              # fetch one page
orenda agent pages put <slug> --title "T" --file page.md   # upsert ('-'/empty = stdin)
orenda agent pages move <slug> --parent <page-id>          # reparent (empty = root)
orenda agent pages backlinks <slug>        # who links here
orenda agent pages delete <slug>           # delete (children cascade)
orenda agent search "query" --type page --limit 5          # FTS5 across pages/tasks/comments
```

Flags → env → config file. Use `-json` for scripts.

`comment` posts to `/api/v1/agent/tasks/{id}/comments`; `await` posts to
`/api/v1/agent/events/await`. Both routes require the bearer token
and write the comment / subscribe the hub under the agent's id
(not the owner's), so the human sees `agent` as the author and
events scoped to this agent only.

---

## 3. The delegation loop — do this, not something else

### 3.1 The eight rules

1. **Always `release` when you give up.** If you claim a task and
   then decide it's not yours (out of scope, blocked reason other
   than dependencies, you crashed mid-run), POST `/release`. Do not
   just walk away. A stuck lock blocks every other agent.

2. **Add a `comment` when you're blocked.** If you can't proceed
   because you need clarification, missing data, or an external
   decision, leave a comment with `comment` and STOP. Don't just
   spin. The user will see the comment and respond. Resume only
   after their reply.

3. **Never submit work that hasn't been (a) reviewed by you and
   (b) referenced from the task description.** "Looks plausible" is
   not "done". If the task description says "implement X", the
   diff / commit / artifact must demonstrate X working.

4. **Submit ≠ done.** After `submit`, the task enters the user's
   review queue. You will hear back via the `/review-queue` endpoint
   (humans) or via a long-poll event (agents). Until the user
   approves, the task is in `review` state — do not start working
   on the next thing assuming success.

5. **Don't hold a claim without progress.** If you need to wait
   (network, long compile, external API), `release` and let the
   scheduler pull the task back when you have signal. Long claim
   timeouts are an anti-pattern.

6. **One task at a time.** The system supports multiple agents but
   the per-agent contract is "one claim, then submit/release".
   Concurrent claims from the same agent will hit the lock.

7. **Respect awaiting mode.** A task with `awaiting='human'` is
   waiting on the user, not on you. Don't re-claim it; check the
   comments for what changed.

8. **Don't write to the database directly.** Everything you can
   do is via the REST API. The CLI is a wrapper; the database is
   private to the server.

### 3.2 The happy path

```bash
# 1. Look for work.
orenda agent next
# → prints: {"task":{"id":"t-1","title":"..."},"ready":true}
# → exit 0 with the task already claimed (200 from /claim).

# 2. Read the snapshot.
orenda agent context t-1 > /tmp/snap.json
# Check status, comments, blocked_by, children.

# 3. Do the work. (Outside this script.)

# 4. Hand it back.
orenda agent submit t-1
# → status=review, awaiting=human. The user sees it in /review.

# 5. If the user returns with a comment, read it, fix, re-submit.
orenda agent context t-1   # see the new comment
# ... fix ...
orenda agent submit t-1

# 6. The user accepts (or returns again). When status=done, you're
#    free to look for the next task.
```

### 3.3 The "no work" exit code

`orenda agent next` exits with code `2` when the ready queue is
empty. This is the contract: bash loops can branch on it.

```bash
while true; do
  orenda agent next || [ $? -eq 2 ] && sleep 30
done
```

`orenda agent await --timeout 60` does the same thing but waits
on the server side (long-poll) instead of polling.

---

## 4. Workflow shapes by use case

### 4.1 Long-running agent

Use `next` in a loop. The loop sleeps on `await` so it doesn't
hammer the API.

```bash
while true; do
  set -e
  orenda agent next
  TASK_ID=$(orenda agent context <last-id> | jq -r .task.id)
  # ... do work ...
  orenda agent submit <task-id>
done
```

### 4.2 One-shot task

You were given a task ID by the user. Run the loop body once.

```bash
TASK_ID="..."
orenda agent claim "$TASK_ID"
orenda agent context "$TASK_ID" > /tmp/snap.json
# ... do work ...
orenda agent submit "$TASK_ID"
```

### 4.3 MCP-native

Run `orenda mcp-proxy` (stdio JSON-RPC 2.0). Tools map to the CLI
subcommands:

```
orenda_me / orenda_list_projects / orenda_list_tasks / orenda_claim
orenda_release / orenda_submit / orenda_context / orenda_await
orenda_task_propose / orenda_pages_list / orenda_pages_get / orenda_pages_save
```

### 4.4 "Build me a course on X" — end-to-end, no human clicks (Phase 29)

The user asks for a course; you deliver it ready to study. The whole
lifecycle is agent-driveable:

```bash
# 1. Create the draft course. Owned by the system owner; no
#    generator task is spawned — YOU are the generator.
curl -s -X POST "$ORENDA_URL/api/v1/agent/courses" \
  -H "Authorization: Bearer $ORENDA_AGENT_TOKEN" -H 'Content-Type: application/json' \
  -d '{"title":"Learn OpenCode","intent_md":"beginner, 30 min/day"}'
# → 201 {"id":"c-1","status":"draft",...}

# 2. Submit the curriculum (modules → lessons → quizzes, one tx).
curl -s -X PUT "$ORENDA_URL/api/v1/agent/courses/c-1/curriculum" \
  -H "Authorization: Bearer $ORENDA_AGENT_TOKEN" -H 'Content-Type: application/json' \
  -d '{"modules":[{"title":"Basics","position":1,"lessons":[
        {"title":"Intro","position":1,"quizzes":[
          {"position":1,"question_md":"2+2?","expected_md":"4","kind":"exact"}]},
        {"title":"Deep dive","position":2}]}]}'
# → course flips draft → review.

# 3. Materialize each lesson (locked → open) and add quizzes.
curl -s -X POST "$ORENDA_URL/api/v1/agent/lessons/<lesson-id>/materialize" \
  -H "Authorization: Bearer $ORENDA_AGENT_TOKEN" -H 'Content-Type: application/json' \
  -d '{"content_md":"# Intro\n..."}'

# 4. Activate: review → active, first lesson unlocked for the student.
curl -s -X POST "$ORENDA_URL/api/v1/agent/courses/c-1/activate" \
  -H "Authorization: Bearer $ORENDA_AGENT_TOKEN"
# → 200 {"status":"active",...}
```

Activation from `draft` is rejected (422) — the curriculum must be
submitted first. The human can still review, request changes, or
archive at any point; activation just removes the mandatory click.

Use the wiki tools to park reference material the course links to:
`orenda_pages_save` a page per topic, then `[[slug]]`-link it from
lesson content.

### 4.5 "Plan my day" — propose study reminders (Phase 31)

The user asks "what should I study today?" You pull the active
courses (with progress), read the planner notes, propose N
reminders, and let the user accept/dismiss them on the Dashboard
tray. The whole loop is opt-in: the platform never schedules
anything on its own.

```bash
# 1. Read the active courses. With ?status=active the rows carry
#    progress + pace_notes_md so we don't round-trip per-course.
curl -s "$ORENDA_URL/api/v1/agent/courses?status=active" \
  -H "Authorization: Bearer $ORENDA_AGENT_TOKEN" | jq '.courses[] | {id, title, pace, pace_notes_md, progress}'
# → e.g. {"id":"c-rust","pace":"regular","pace_notes_md":"3 times a week, mornings",
#         "progress":{"lessons_total":12,"lessons_done":7,"open_lessons":[...],
#                     "last_completed_at":"2026-08-18T...",
#                     "pace":{"since":"...","window_days":14,
#                             "lessons_done_in_window":3,
#                             "actual_velocity_per_week":1.5,
#                             "target_velocity_per_week":1.0,
#                             "drift":"ahead"}}}

# 2a. Phase 32.12 (LMS pace adaptation): scale your proposal cadence
#     by `progress.pace.drift`. The classifier compares the user's
#     actual lesson-completion velocity against their own accepted
#     proposal rate over a 14-day rolling window:
#       - drift="ahead"    → user is moving faster than their accepted
#                            proposal rate. File FEWER proposals this
#                            run (or none); they're not the bottleneck.
#       - drift="behind"   → user accepted proposals but hasn't finished
#                            the lessons. File MORE proposals (or break
#                            the backlog into smaller next-action-sized
#                            chunks) to nudge them back on pace.
#       - drift="on_track" → file proposals as usual. The classifier
#                            only escalates when the gap is ≥ ±30%
#                            over the window. Both-zero (no proposals,
#                            no completions yet) is also on_track —
#                            don't panic without evidence.
#     This is the load-bearing planner behaviour the SKILL describes;
#     the `progress.pace.drift` value is the only signal that drives
#     how many study-reminders to file per "Plan my day" run.

# 2. Read pace_notes_md + progress. Pick a subset of open_lessons
#    that fits the user's stated cadence (e.g. "study 1 lesson/day").
#    Compose one proposal per lesson, with the lesson title as the
#    proposal title and the body_md containing a 1-line why-now.

# 3. File each proposal — one POST per reminder.
for lesson in "${LESSONS[@]}"; do
  curl -s -X POST "$ORENDA_URL/api/v1/agent/study-proposals" \
    -H "Authorization: Bearer $ORENDA_AGENT_TOKEN" -H 'Content-Type: application/json' \
    -d "{\"course_id\":\"c-rust\",\"title\":\"$lesson\",\"body_md\":\"$lesson notes\",\"target_date\":\"$(date +%Y-%m-%d)\"}"
done
# → 201 per call; the user's Dashboard tray now shows them.

# 4. The user reviews the tray and accepts/dismisses each one.
#    (This happens in the UI; you do NOT call accept/dismiss — that's
#    a user-only endpoint.) The accepted reminder becomes an inbox
#    task with study_course_id set and due_at = max(target_date, today).
```

The plan loop is read-only from the agent's side until the
proposal POSTs land; after that, the user takes over the
accept/dismiss decisions. Don't try to bypass the tray — the
opt-in pattern is the whole point.

#### Idempotency

`POST /api/v1/agent/study-proposals` always creates a new pending
proposal (the planner may want to revise a target date by filing a
new one). Accept is idempotent on the user side: re-accepting the
same proposal returns the existing task id (200, not 201).

#### Don't escalate reminders to "overdue"

A study reminder has `study_course_id` set; the Today screen reads
this and never surfaces reminders under `overdue`. A missed day
doesn't turn red — the user can still ack it on the tray today.

#### When to skip proposing

- The user said "no reminders today" earlier — read their
  intent from comments/notes, not from this skill.
- The course is `done` or `archived` — `GET ...?status=active`
  already filters those.
- All open lessons are completed (`progress.lessons_done ==
  progress.lessons_total`) — there's nothing to suggest.

### 4.6 "File new work" — propose a task (Phase 33.1)

You found work that isn't in the instance yet (a gap discovered while
closing another task, a follow-up the user asked for). Don't park it
in a comment or a private note — file it as a task:

```bash
orenda agent propose --project <project-id> \
  --title "Verb + object" --description-file spec.md
# → 201 {"id":"t-x","status":"backlog","awaiting":"human",...}
```

The task lands as `status=backlog, awaiting=human` — it shows up in
the owner's review queue (`GET /api/v1/review-queue`) for triage.
**You do not work on it until the human accepts**: accept = the owner
moves the card to `todo` (which clears `awaiting`), dismiss = delete.
After triage the task appears in your `GET /api/v1/agent/tasks?ready=true`
like any other claimable task.

Required fields: `project_id`, `title`, `description_md` (the task must
be self-sufficient — see rule 3). Optional: `priority`,
`blocked_by` (task ids), `parent_task_id`. MCP equivalent:
`orenda_task_propose`.

---

## 5. Common errors

| Symptom | Cause | Fix |
|---|---|---|
| `HTTP 409 lock_taken` | Another agent claims the task. | Pick a different task. Don't retry. |
| `HTTP 422 task_blocked` + `unfinished_blockers` | Phase 15 dep tree has unfinished blockers. | Wait, or request a different task. |
| `HTTP 429 Too Many Requests` | Rate limited. | Back off; the `Retry-After` header tells you how long. |
| `HTTP 401` | Token expired or revoked. | Re-register the agent. |
| `orenda agent: --url is required` | Not configured. | Set `ORENDA_URL` + `ORENDA_AGENT_TOKEN` or write `~/.config/orenda/agent.yaml`. |

---

## 6. Reference

### 6.1 Endpoints (agent namespace)

| Method | Path | Purpose |
|---|---|---|
| GET | `/api/v1/agent/me` | Confirm the token, see scan state. |
| POST | `/api/v1/agent/heartbeat` | Mark online; refresh `last_seen_at`. |
| GET | `/api/v1/agent/tasks?ready=true&limit=N` | List claimable tasks. |
| GET | `/api/v1/agent/projects` | List all projects (single-owner) — your source of `project_id` for `propose`; the name feeds branch naming. |
| POST | `/api/v1/agent/tasks` | Phase 33.1: propose a NEW task. Body: `{project_id, title, description_md, priority?, blocked_by?, parent_task_id?}`. Lands `backlog` + `awaiting=human` (the owner's review queue triages it). 400 on missing required fields, 404 on unknown project/parent/blocker. |
| POST | `/api/v1/agent/tasks/{id}/claim` | Atomic claim. 409 / 422 on failure. |
| POST | `/api/v1/agent/tasks/{id}/release` | Drop a claim. |
| POST | `/api/v1/agent/tasks/{id}/submit` | Mark ready for human review. |
| GET | `/api/v1/agent/tasks/{id}/context` | Full snapshot: task + comments + activity + children + checklists. |
| GET | `/api/v1/agent/courses?status=draft` | Phase 18: courses the tutor can claim. |
| POST | `/api/v1/agent/courses` | Phase 29.4: create a draft course (owner = system owner, no generator task — you are the generator). |
| POST | `/api/v1/agent/courses/{id}/activate` | Phase 29.5: review → active (same transition as the owner's Approve click). |
| PUT | `/api/v1/agent/courses/{id}/curriculum` | Phase 18: tutor's atomic curriculum swap. Phase 27.6: the payload now carries per-lesson `quizzes` (`{position, question_md, expected_md?, kind: 'exact'|'open'}`) and per-module `description`; submit the whole program in one tx. |
| POST | `/api/v1/agent/lessons/{id}/materialize` | Phase 27.4: tutor writes lesson content (`content_md`, optional `task_id`); lesson flips locked → open. |
| PUT | `/api/v1/agent/lessons/{id}/content` | Phase 27.4: in-place content update (same handler). |
| POST | `/api/v1/agent/lessons/{id}/quizzes` | Phase 27.6 (closes Phase 18.6): append a single quiz to an existing lesson without re-submitting the whole curriculum. |
| POST | `/api/v1/agent/tasks/{id}/comments` | Add a comment authored by the agent (Phase 27.11). |
| POST | `/api/v1/agent/events/await` | Long-poll for events scoped to the agent's id (Phase 27.11; timeout ≤ 60s). |
| GET | `/api/v1/agent/pages` | Phase 29.1: wiki page tree. |
| GET | `/api/v1/agent/pages/{slug}` | Fetch one page. |
| PUT | `/api/v1/agent/pages/{slug}` | Upsert a page (`{title, content_md, parent_id?}`). `[[slug]]` links are indexed on save. |
| DELETE | `/api/v1/agent/pages/{slug}` | Delete a page (children cascade). |
| PATCH | `/api/v1/agent/pages/{slug}/move` | Reparent (`{parent_id}`, empty = root). |
| GET | `/api/v1/agent/pages/{slug}/backlinks` | Pages linking here. |
| GET | `/api/v1/agent/search?q=&type=&limit=` | FTS5 across pages/tasks/comments. |
| GET | `/api/v1/agent/courses?status=active` | Phase 31.5: list courses. With `?status=active` the row carries a `progress` sub-object (lessons_total / lessons_done / open_lessons[]) and `pace_notes_md` so the planner has everything in one round-trip. |
| POST | `/api/v1/agent/courses/{id}/curriculum` | (Also Phase 18 — see above.) |
| POST | `/api/v1/agent/courses/{id}/activate` | (Also Phase 29.5 — see above.) |
| PATCH | `/api/v1/agent/courses/{id}` | Phase 31.5: narrow update of `pace_notes_md` only. The body's `pace_notes_md` is trimmed + capped at 64 KiB by `course.Course.Validate`. |
| POST | `/api/v1/agent/study-proposals` | Phase 31.5: file a pending study proposal. Body: `{course_id?, title, body_md?, target_date (YYYY-MM-DD)}`. The Dashboard tray picks it up. Created by the planner; the user accepts or dismisses. |

### 6.2 Common task fields

| Field | Type | Notes |
|---|---|---|
| `id` | UUIDv7 | Stable identifier. |
| `status` | enum | `backlog \| todo \| in_progress \| review \| done` |
| `awaiting` | enum | `none \| human \| agent` — who must act next. |
| `assignee_type` | enum | `user \| agent` |
| `assignee_id` | UUID | The bound id. |
| `priority` | enum | `low \| medium \| high \| urgent` |
| `blocked_by` | array of UUID | Phase 15. Open blockers only. |
| `due_at` | ISO 8601 | Optional. |
| `tags` | array | Phase 13. |
| `counters` | object | Phase 17. comments/attachments/children/checklist. |
