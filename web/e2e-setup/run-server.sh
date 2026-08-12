#!/usr/bin/env bash
# E2E test fixture runner — invoked by Playwright's `webServer.command`.
#
# Why a script:
#   Playwright runs `webServer` BEFORE `globalSetup`, so a global-setup that
#   prepares the DB would never get a chance to run when the server is the
#   thing that needs the DB. We therefore fold the one-time setup (clean
#   tmp dir, migrate up, seed user) into the server boot itself.
#
# Why we cd to the project root:
#   The default Go build embeds an empty placeholder FS; the SPA handler
#   falls back to `web/dist` on disk, which is only resolvable when the
#   binary's CWD is the project root. (`make build -tags=web_dist` is the
#   other way to embed the assets, but that requires touching the Makefile
#   which is out of scope here.)
#
# Exit code is whatever orenda serve returns — Playwright's readiness probe
# waits on /healthz and reports failure with the server's stderr.
set -euo pipefail

# Resolve the project root from this script's location:
#   web/e2e-setup/run-server.sh → ../.. (the worktree root)
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"
cd "${PROJECT_ROOT}"

E2E_DIR="/tmp/orenda-e2e"
DATA_DIR="${E2E_DIR}/data"
DB_PATH="${DATA_DIR}/orenda.db"
BIN="${BIN_PATH:-./bin/orenda}"

# Idempotent: wipe so a previous crashed run doesn't leak migrations.
rm -rf "${E2E_DIR}"
mkdir -p "${DATA_DIR}"

export ORENDA_STORAGE__DATA_DIR="${DATA_DIR}"
export ORENDA_STORAGE__DB_PATH="${DB_PATH}"
export ORENDA_AUTH__JWT_SECRET="e2e-secret-not-for-production"
export ORENDA_LOGGING__LEVEL="${ORENDA_LOG_LEVEL:-warn}"

# Phase 26.E: bump the per-user rate limit so the Playwright suite
# (which fires ~50+ auth'd requests per spec) doesn't trip 429s
# during multi-spec runs. Production defaults in router.go are
# unchanged when these env vars are unset.
# Phase 26.E: hard-set the per-user rate limits so the Playwright suite
# (which fires ~50+ auth'd requests per spec) doesn't trip 429s
# during multi-spec runs. Production defaults in router.go are
# unchanged when these env vars are unset.
export ORENDA_RATELIMIT_AUTH_BURST="1000000"
export ORENDA_RATELIMIT_AUTH_PER_SEC="100000"
# The API request contexts in tests don't carry cookies, so all
# helper-driven calls hit the anon bucket per-IP. Bump it the same way.
export ORENDA_RATELIMIT_ANON_BURST="1000000"
export ORENDA_RATELIMIT_ANON_PER_SEC="100000"
echo "[e2e-server] effective auth rate limit: burst=$ORENDA_RATELIMIT_AUTH_BURST per_sec=$ORENDA_RATELIMIT_AUTH_PER_SEC"
echo "[e2e-server] verifying env..."
printenv | grep -i ratelimit || echo "(no ratelimit env)"

echo "[e2e-server] migrate up..."
"${BIN}" migrate up >/dev/null

echo "[e2e-server] seed user..."
printf 'testpass123\n' | "${BIN}" user create \
  --email e2e@orenda.local \
  --display-name "E2E User" \
  --password-stdin >/dev/null

echo "[e2e-server] starting orenda serve on port ${ORENDA_SERVER__PORT:-21371}..."
exec "${BIN}" serve
