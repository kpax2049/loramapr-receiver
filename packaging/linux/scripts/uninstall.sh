#!/usr/bin/env bash
set -euo pipefail

BINARY="${BINARY:-/usr/bin/loramapr-receiverd}"
TARGET_ROOT="${TARGET_ROOT:-/}"

if [[ $# -gt 0 && "$1" == /* ]]; then
  BINARY="$1"
  shift
fi

echo "Uninstalling LoRaMapr Receiver from ${TARGET_ROOT}"
"$BINARY" uninstall --target-root "$TARGET_ROOT" "$@"

echo "If running on host root, disable service manually if still active:"
echo "  sudo systemctl disable --now loramapr-receiverd"
