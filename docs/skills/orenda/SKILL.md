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
orenda agent next          # await + claim a task in one shot
orenda agent claim <id>    # claim a specific task by id
orenda agent context <id>  # read the full task snapshot
orenda agent comment <id> "...markdown..."   # ⚠ see known issue below
orenda agent submit <id>   # mark ready for human review
orenda agent release <id>  # give up a claim
orenda agent await         # long-poll for the next event
```

Flags → env → config file. Use `-json` for scripts.

> ⚠ **Known issue (Phase 27.11):** `orenda agent comment` and `orenda agent
> await` currently hit user-only routes (`POST /tasks/{id}/comments` and
> `POST /events/await` live under cookie auth), so an agent token gets 401.
> Agent-namespace aliases are being added. Claim/submit/context/next are
> unaffected.

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
orenda_me / orenda_list_tasks / orenda_claim / orenda_release
orenda_submit / orenda_context / orenda_await
```

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
| POST | `/api/v1/agent/tasks/{id}/claim` | Atomic claim. 409 / 422 on failure. |
| POST | `/api/v1/agent/tasks/{id}/release` | Drop a claim. |
| POST | `/api/v1/agent/tasks/{id}/submit` | Mark ready for human review. |
| GET | `/api/v1/agent/tasks/{id}/context` | Full snapshot: task + comments + activity + children + checklists. |
| GET | `/api/v1/agent/courses?status=draft` | Phase 18: courses the tutor can claim. |
| PUT | `/api/v1/agent/courses/{id}/curriculum` | Phase 18: tutor's atomic curriculum swap. Phase 27.6: the payload now carries per-lesson `quizzes` (`{position, question_md, expected_md?, kind: 'exact'|'open'}`) and per-module `description`; submit the whole program in one tx. |
| POST | `/api/v1/agent/lessons/{id}/materialize` | Phase 27.4: tutor writes lesson content (`content_md`, optional `task_id`); lesson flips locked → open. |
| PUT | `/api/v1/agent/lessons/{id}/content` | Phase 27.4: in-place content update (same handler). |
| POST | `/api/v1/agent/lessons/{id}/quizzes` | Phase 27.6 (closes Phase 18.6): append a single quiz to an existing lesson without re-submitting the whole curriculum. |
| POST | `/api/v1/events/await` | Long-poll for events (timeout ≤ 60s). ⚠ Currently user-auth only — see the known issue in §2.2. |

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
