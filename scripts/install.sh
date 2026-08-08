#!/usr/bin/env bash
# Orenda installer — builds the binary and installs it to ~/.local/bin.
#
# Usage:
#   scripts/install.sh           # build + install
#   scripts/install.sh --systemd # also install the user systemd unit
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_ROOT"

INSTALL_DIR="${ORENDA_INSTALL_DIR:-$HOME/.local/bin}"
DATA_DIR="${ORENDA_DATA_DIR:-$HOME/.local/share/orenda}"
WITH_SYSTEMD=0
[[ "${1:-}" == "--systemd" ]] && WITH_SYSTEMD=1

echo "==> Building Orenda"
make build

echo "==> Installing binary to $INSTALL_DIR"
mkdir -p "$INSTALL_DIR"
install -m 0755 bin/orenda "$INSTALL_DIR/orenda"

echo "==> Preparing data dir at $DATA_DIR"
mkdir -p "$DATA_DIR"
if [[ ! -f "$DATA_DIR/config.yaml" ]]; then
  sed "s|data/|$DATA_DIR/|g" data/config.example.yaml > "$DATA_DIR/config.yaml"
  echo "    wrote $DATA_DIR/config.yaml"
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
echo "  ORENDA_AUTH__JWT_SECRET=\$(head -c32 /dev/urandom | base64) orenda user create \\"
echo "      --email you@example.com --display-name You --password-stdin \\"
echo "      --config $DATA_DIR/config.yaml"