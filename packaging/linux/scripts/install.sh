#!/usr/bin/env bash
set -euo pipefail

BINARY="${BINARY:-./bin/loramapr-receiverd}"
TARGET_ROOT="${TARGET_ROOT:-/}"

if [[ $# -gt 0 && "$1" == /* ]]; then
  BINARY="$1"
  shift
fi

if [[ ! -x "$BINARY" ]]; then
  echo "Binary not executable: $BINARY" >&2
  exit 1
fi

echo "Installing LoRaMapr Receiver into ${TARGET_ROOT}"
"$BINARY" install --target-root "$TARGET_ROOT" --force "$@"

echo "Install files written. To enable service on host root:"
echo "  sudo systemctl daemon-reload"
echo "  sudo systemctl enable --now loramapr-receiverd"
