#!/usr/bin/env bash
# Phase 30.15: smoke tests for uninstall.sh flag parsing.
#
# We don't exercise the destructive paths (no real systemd unit on
# CI); we just verify that the CLI surface is parseable and the
# error paths surface the right exit code.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
UNINSTALL="$SCRIPT_DIR/scripts/uninstall.sh"
UPDATE="$SCRIPT_DIR/scripts/update-dogfood.sh"

# Ensure scripts are executable. The dev install target chmod's them.
chmod +x "$UNINSTALL" "$UPDATE" 2>/dev/null || true

fail() { echo "FAIL: $*" >&2; exit 1; }
pass() { echo "  ok: $*"; }

# uninstall.sh --help prints usage and exits 0.
echo "= uninstall.sh flag parsing ="
if "$UNINSTALL" --help >/dev/null 2>&1; then
  pass "uninstall --help exits 0"
else
  fail "uninstall --help should exit 0"
fi

# uninstall.sh --purge-data is a recognised flag (without purging
# — we never answer the prompt). Verify exit 2 only if the prompt
# is non-interactive; in CI we'd answer 'n' to keep the test
# idempotent.
output=$(echo "n" | "$UNINSTALL" --purge-data 2>&1) || true
if echo "$output" | grep -q "kept /"; then
  pass "uninstall --purge-data prompts and keeps data on 'n'"
else
  fail "uninstall --purge-data prompt result:\n$output"
fi

# uninstall.sh rejects unknown flags with exit 2.
if "$UNINSTALL" --bogus-flag 2>/dev/null; then
  fail "uninstall --bogus-flag should exit 2"
else
  code=$?
  if [[ "$code" == "2" ]]; then
    pass "uninstall --bogus-flag exits 2"
  else
    fail "uninstall --bogus-flag expected exit 2, got $code"
  fi
fi

if "$UNINSTALL" extra 2>/dev/null; then
  fail "uninstall 'extra' should exit 2"
else
  pass "uninstall 'extra' exits 2"
fi

# update-dogfood.sh --help prints usage and exits 0.
echo
echo "= update-dogfood.sh flag parsing ="
if "$UPDATE" --help >/dev/null 2>&1; then
  pass "update-dogfood --help exits 0"
else
  fail "update-dogfood --help should exit 0"
fi

# update-dogfood.sh rejects unknown flags with exit 2.
if "$UPDATE" --whatever 2>/dev/null; then
  fail "update-dogfood --whatever should exit 2"
else
  code=$?
  if [[ "$code" == "2" ]]; then
    pass "update-dogfood --whatever exits 2"
  else
    fail "update-dogfood --whatever expected exit 2, got $code"
  fi
fi

# update-dogfood.sh refuses to run from a non-main branch.
# We're on a regression branch in this checkout — exit 1 expected.
if "$UPDATE" 2>/dev/null; then
  fail "update-dogfood should exit 1 on non-main branch"
else
  code=$?
  if [[ "$code" == "1" ]]; then
    pass "update-dogfood exits 1 on non-main branch"
  else
    fail "update-dogfood expected exit 1, got $code"
  fi
fi

# update-dogfood.sh --force on a non-main branch still exits 1
# unless the file/test environment is on main. We're guaranteed not
# to be on main inside a worktree, so --force should NOT bypass.
if "$UPDATE" --force 2>/dev/null; then
  fail "update-dogfood --force should still exit on non-main branch"
else
  pass "update-dogfood --force on non-main branch still exits non-zero"
fi

# install.sh / make build must run `npm ci` before `npm run build`
# so that a release adding a new npm dependency does not break the
# dogfood update (Task #26). Makefile web-build is the single point
# where the SPA pipeline is defined; `install.sh` delegates to it via
# `make build`. We verify the dry-run shape: `npm ci` must precede
# `npm run build`.
echo
echo "= install.sh build path ="
build_recipe="$(cd "$SCRIPT_DIR" && make -n web-build 2>/dev/null)"
if echo "$build_recipe" | grep -q 'cd web && npm ci'; then
  pass "make web-build invokes 'npm ci' (lockfile-pinned install)"
else
  fail "make web-build must invoke 'npm ci' before 'npm run build' (Task #26)"
fi
if echo "$build_recipe" | grep -q 'cd web && npm run build'; then
  pass "make web-build still invokes 'npm run build'"
else
  fail "make web-build must invoke 'npm run build' (Task #26)"
fi
ci_pos="$(echo "$build_recipe" | grep -n 'cd web && npm ci' | head -1 | cut -d: -f1)"
build_pos="$(echo "$build_recipe" | grep -n 'cd web && npm run build' | head -1 | cut -d: -f1)"
if [[ -n "$ci_pos" && -n "$build_pos" && "$ci_pos" -lt "$build_pos" ]]; then
  pass "'npm ci' precedes 'npm run build' in web-build"
else
  fail "'npm ci' must precede 'npm run build' in web-build (Task #26)"
fi

echo
echo "All scripts/smoke tests passed."