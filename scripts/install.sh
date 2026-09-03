#!/usr/bin/env bash
# Orenda installer — builds the binary and installs it to ~/.local/bin.
#
# Usage:
#   scripts/install.sh           # build + install
#   scripts/install.sh --systemd # also install the user systemd unit
#   scripts/install.sh --force   # bypass the channel guard (see below)
#
# Channel guard (Phase 28.20): this script is the *only* sanctioned way
# to update the usage/dogfood instance on :2137. It refuses to install
# unless the working tree is on `main` with a clean status. The intent:
# `git pull` in ~/opt/orenda tracks origin/main and nothing else; if you
# `git checkout dev` in there, you can never accidentally install an
# unreleased build into your daily-use channel. `--force` is a deliberate
# override for emergencies; the script still tells you what you're doing.
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_ROOT"

INSTALL_DIR="${ORENDA_INSTALL_DIR:-$HOME/.local/bin}"
DATA_DIR="${ORENDA_DATA_DIR:-$HOME/.local/share/orenda}"

# Parse flags. Order-independent (--systemd and --force can appear in
# any combination).
WITH_SYSTEMD=0
FORCE=0
for arg in "$@"; do
  case "$arg" in
    --systemd) WITH_SYSTEMD=1 ;;
    --force)   FORCE=1 ;;
    -h|--help)
      sed -n '2,16p' "$0" | sed 's/^# \{0,1\}//'
      exit 0
      ;;
    *)
      echo "ERROR: unknown flag: $arg" >&2
      echo "Try: scripts/install.sh [--systemd] [--force]" >&2
      exit 2
      ;;
  esac
done

# Channel guard. We only require git; everything else (build, install,
# systemd wiring) assumes the guard passed.
if command -v git >/dev/null 2>&1; then
  CURRENT_BRANCH="$(git rev-parse --abbrev-ref HEAD 2>/dev/null || echo unknown)"
  CURRENT_HASH="$(git rev-parse --short HEAD 2>/dev/null || echo unknown)"
  DIRTY="$(git status --porcelain 2>/dev/null || true)"

  echo "==> Channel: $CURRENT_BRANCH @ $CURRENT_HASH (dirty: $([[ -n "$DIRTY" ]] && echo yes || echo no))"
  echo "    Binary:  $INSTALL_DIR/orenda"
  echo "    Data:    $DATA_DIR/"

  if [[ "$CURRENT_BRANCH" != "main" || -n "$DIRTY" ]]; then
    if [[ "$FORCE" -ne 1 ]]; then
      echo
      echo "ERROR: refusing to install from a non-main or dirty checkout." >&2
      echo "       Usage/dogfood channel must be on 'main' with a clean tree." >&2
      echo "       Override: scripts/install.sh --force" >&2
      exit 1
    fi
    echo "    --force: bypassing channel guard."
  fi
else
  echo "==> Channel: (no git in PATH — skipping guard)"
fi

echo "==> Building Orenda"
make build

echo "==> Installing binary to $INSTALL_DIR"
mkdir -p "$INSTALL_DIR"
install -m 0755 bin/orenda "$INSTALL_DIR/orenda"

echo "==> Preparing data dir at $DATA_DIR"
mkdir -p "$DATA_DIR"
if [[ ! -f "$DATA_DIR/config.yaml" ]]; then
  sed "s|data/|$DATA_DIR/|g" configs/config.example.yaml > "$DATA_DIR/config.yaml"
  echo "    wrote $DATA_DIR/config.yaml"
fi

# JWT secret (Phase 28.21): the systemd unit reads @DATADIR@/env via
# EnvironmentFile. Task 138: fresh installs store the secret in
# @DATADIR@/credentials/jwt (mode 600, dir 700) and the env file only
# carries the *_FILE pointer, so the secret never lands in
# /proc/*/environ. Existing installs keep their current env file
# untouched (backward compatibility).
if [[ ! -f "$DATA_DIR/env" ]]; then
  mkdir -p "$DATA_DIR/credentials"
  chmod 700 "$DATA_DIR/credentials"
  umask 077
  head -c32 /dev/urandom | base64 | tr -d '\n' > "$DATA_DIR/credentials/jwt"
  chmod 600 "$DATA_DIR/credentials/jwt"
  printf 'ORENDA_AUTH__JWT_SECRET_FILE=%s/credentials/jwt\n' "$DATA_DIR" > "$DATA_DIR/env"
  chmod 600 "$DATA_DIR/env"
  echo "    wrote $DATA_DIR/credentials/jwt (random JWT secret, mode 600)"
  echo "    wrote $DATA_DIR/env (points at the secret file, mode 600)"
fi

if [[ "$WITH_SYSTEMD" == "1" ]]; then
  UNIT_DIR="$HOME/.config/systemd/user"
  mkdir -p "$UNIT_DIR"
  sed -e "s|@BINDIR@|$INSTALL_DIR|g" \
      -e "s|@DATADIR@|$DATA_DIR|g" \
      scripts/systemd/orenda.service > "$UNIT_DIR/orenda.service"
  systemctl --user daemon-reload
  systemctl --user enable --now orenda.service
  echo "==> systemd user unit installed and started"
  echo "    systemctl --user status orenda"
fi

echo
echo "Done. Try:"
echo "  orenda version"
echo "  orenda serve --config $DATA_DIR/config.yaml"
echo "  set -a; . $DATA_DIR/env; set +a   # loads ORENDA_AUTH__JWT_SECRET_FILE (secret stays in $DATA_DIR/credentials/jwt)"
echo "  orenda user create \\"
echo "      --email you@example.com --display-name You --password-stdin \\"
echo "      --config $DATA_DIR/config.yaml"