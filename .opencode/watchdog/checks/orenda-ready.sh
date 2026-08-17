#!/usr/bin/env bash
# orenda-ready.sh — watchdog check for the Orenda usage instance agent queue.
#
# Fires (exit 0) while GET /api/v1/agent/tasks?ready=true returns count > 0,
# printing task titles to stdout. Stateless by itself — the watchdog plugin's
# edge-trigger turns the level into a single notification.
#
# Env: ORENDA_AGENT_TOKEN (preferred), ORENDA_URL (default http://127.0.0.1:2137).
# Token fallback: if ORENDA_AGENT_TOKEN is unset, the token is read from the
# operator's own opencode MCP config (~/.config/opencode/opencode.json →
# mcp.orenda.command, the argument after "--token") — the same credential the
# orenda MCP tools use. The token is never written to the tracked config.

set -euo pipefail

BASE="${ORENDA_URL:-http://127.0.0.1:2137}"
TOKEN="${ORENDA_AGENT_TOKEN:-}"
if [ -z "$TOKEN" ]; then
  cfg="$HOME/.config/opencode/opencode.json"
  if [ -f "$cfg" ]; then
    TOKEN=$(perl -0777 -ne 'print $1 if /"--token",\s*"([^"]+)"/' "$cfg" || true)
  fi
fi
[ -n "$TOKEN" ] || { echo "orenda-ready: ORENDA_AGENT_TOKEN is not set and no --token found in opencode MCP config" >&2; exit 2; }

resp=$(curl -sf --max-time 10 -H "Authorization: Bearer $TOKEN" "$BASE/api/v1/agent/tasks?ready=true") \
  || { echo "orenda-ready: request to $BASE failed" >&2; exit 2; }

count=$(printf '%s' "$resp" | perl -ne 'print $1 if /"count"\s*:\s*(\d+)/')
count="${count:-0}"

if [ "$count" -gt 0 ]; then
  printf '%s' "$resp" | perl -ne 'print "- $1\n" while /"title"\s*:\s*"((?:[^"\\]|\\.)*)"/g'
  exit 0
fi
exit 1
