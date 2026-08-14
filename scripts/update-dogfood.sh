#!/usr/bin/env bash
# update-dogfood.sh — refresh the usage/dogfood instance.
#
# This script exists to make "update the channel I'm actually using" a
# single command. Run it from ~/opt/orenda (the usage checkout) and it
# pulls the latest from origin/main, reinstalls the binary, and
# restarts the systemd unit. The pull is `--ff-only` so the channel
# stays linear: no merge commits, no diverged history, no surprises.
#
# Pre-flight: refuses to run unless we're on `main` with a clean tree.
# The whole point of the dev/dogfood split (Phase 28.20) is that the
# usage channel never sees unreleased code by accident; this script
# encodes that rule as the default.
#
# Usage:
#   ./scripts/update-dogfood.sh
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_ROOT"

CURRENT_BRANCH="$(git rev-parse --abbrev-ref HEAD)"
CURRENT_HASH="$(git rev-parse --short HEAD)"

if [[ "$CURRENT_BRANCH" != "main" ]]; then
  echo "ERROR: update-dogfood must run from a checkout on 'main'" >&2
  echo "       (currently on '$CURRENT_BRANCH' @ $CURRENT_HASH)" >&2
  exit 1
fi

if [[ -n "$(git status --porcelain)" ]]; then
  echo "ERROR: working tree is dirty; refusing to update." >&2
  echo "       Commit or stash your changes first." >&2
  git status --porcelain
  exit 1
fi

echo "==> Pulling latest from origin/main"
git pull --ff-only origin main

echo "==> Reinstalling"
scripts/install.sh --systemd

echo "==> Restarting service"
systemctl --user restart orenda

echo
echo "Done. systemd restarted; new binary live."
echo "  systemctl --user status orenda"
echo "  journalctl --user -u orenda -n 20"
