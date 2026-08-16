#!/usr/bin/env bash
# Orenda uninstaller — removes the binary, the user unit, and (optionally)
# the data directory.
set -euo pipefail

INSTALL_DIR="${ORENDA_INSTALL_DIR:-$HOME/.local/bin}"
DATA_DIR="${ORENDA_DATA_DIR:-$HOME/.local/share/orenda}"
PURGE_DATA=0
[[ "${1:-}" == "--purge-data" ]] && PURGE_DATA=1

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