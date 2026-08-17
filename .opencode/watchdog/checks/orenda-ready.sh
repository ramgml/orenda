#!/usr/bin/env bash
# orenda-ready.sh — watchdog check for the Orenda usage instance agent queue.
#
# Fires (exit 0) while GET /api/v1/agent/tasks?ready=true returns count > 0,
# printing task titles to stdout. Stateless by itself — the watchdog plugin's
# edge-trigger turns the level into a single notification.
#
# Env: ORENDA_AGENT_TOKEN (required), ORENDA_URL (default http://127.0.0.1:2137).
#
# Exit codes: 0 = queue non-empty; 1 = queue empty; 2 = misconfigured/unreachable.

set -euo pipefail

BASE="${ORENDA_URL:-http://127.0.0.1:2137}"
TOKEN="${ORENDA_AGENT_TOKEN:-}"
[ -n "$TOKEN" ] || { echo "orenda-ready: ORENDA_AGENT_TOKEN is not set" >&2; exit 2; }

resp=$(curl -sf --max-time 10 -H "Authorization: Bearer $TOKEN" "$BASE/api/v1/agent/tasks?ready=true") \
  || { echo "orenda-ready: request to $BASE failed" >&2; exit 2; }

count=$(printf '%s' "$resp" | perl -ne 'print $1 if /"count"\s*:\s*(\d+)/')
count="${count:-0}"

if [ "$count" -gt 0 ]; then
  printf '%s' "$resp" | perl -ne 'print "- $1\n" while /"title"\s*:\s*"((?:[^"\\]|\\.)*)"/g'
  exit 0
fi
exit 1
