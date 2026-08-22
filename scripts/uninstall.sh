#!/usr/bin/env bash
# Orenda uninstaller — removes the binary, the user unit, and (optionally)
# the data directory.
#
# Usage:
#   scripts/uninstall.sh                # remove binary + unit, keep data
#   scripts/uninstall.sh --purge-data   # also prompt to delete the data dir
#   scripts/uninstall.sh -h | --help    # print this help and exit 0
#
# Unknown flags OR any positional argument abort with exit 2 and a
# usage hint on stderr — BEFORE any systemctl call is made. Task #28.
set -euo pipefail

INSTALL_DIR="${ORENDA_INSTALL_DIR:-$HOME/.local/bin}"
DATA_DIR="${ORENDA_DATA_DIR:-$HOME/.local/share/orenda}"
PURGE_DATA=0

usage() {
  cat <<'EOF' >&2
Usage: scripts/uninstall.sh [OPTIONS]

Removes the orenda binary, the user systemd unit (if installed), and
(optionally) the data directory.

Options:
  --purge-data    After stopping the service, prompt to delete the data
                  directory (includes the database). Defaults to keep.
  -h, --help      Print this help and exit 0.

Any unknown flag or extra positional argument aborts with exit 2; no
systemctl call is made on the error path.
EOF
}

# Strict argument parsing — every code path that touches systemctl lives
# AFTER this loop, so a parse error never reaches the unit.
while [[ $# -gt 0 ]]; do
  case "$1" in
    -h|--help)
      usage
      exit 0
      ;;
    --purge-data)
      PURGE_DATA=1
      shift
      ;;
    --)
      shift
      if [[ $# -gt 0 ]]; then
        echo "ERROR: unexpected positional arguments: $*" >&2
        usage >&2
        exit 2
      fi
      break
      ;;
    -*)
      echo "ERROR: unknown flag: $1" >&2
      usage >&2
      exit 2
      ;;
    *)
      echo "ERROR: unexpected positional argument: $1" >&2
      usage >&2
      exit 2
      ;;
  esac
done

echo "==> Stopping systemd unit (if running)"
systemctl --user disable --now orenda.service 2>/dev/null || true
rm -f "$HOME/.config/systemd/user/orenda.service"
systemctl --user daemon-reload 2>/dev/null || true

echo "==> Removing binary"
rm -f "$INSTALL_DIR/orenda"

if [[ "$PURGE_DATA" == "1" ]]; then
  read -r -p "Delete $DATA_DIR (includes the database)? [y/N] " ans
  if [[ "$ans" =~ ^[Yy]$ ]]; then
    rm -rf "$DATA_DIR"
    echo "    removed $DATA_DIR"
  else
    echo "    kept $DATA_DIR"
  fi
else
  echo "==> Data directory preserved at $DATA_DIR"
fi

echo "Done."