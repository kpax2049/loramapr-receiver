#!/usr/bin/env bash
set -euo pipefail

CHANNEL="stable"
BASE_URL="https://downloads.loramapr.com/apt"
PACKAGE_NAME="loramapr-receiver"
KEYRING_PATH="/usr/share/keyrings/loramapr-archive-keyring.gpg"
LIST_PATH="/etc/apt/sources.list.d/loramapr-receiver.list"

usage() {
  cat <<'EOF'
Usage:
  bootstrap-apt.sh [--channel stable|beta] [--base-url <apt-root>] [--package <name>]

Examples:
  sudo ./bootstrap-apt.sh
  sudo ./bootstrap-apt.sh --channel beta
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --channel)
      CHANNEL="${2:-}"
      shift 2
      ;;
    --base-url)
      BASE_URL="${2:-}"
      shift 2
      ;;
    --package)
      PACKAGE_NAME="${2:-}"
      shift 2
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "Unknown argument: $1" >&2
      usage >&2
      exit 1
      ;;
  esac
done

if [[ "$(id -u)" -ne 0 ]]; then
  echo "This script must run as root. Re-run with sudo." >&2
  exit 1
fi

if [[ -z "${CHANNEL}" ]]; then
  echo "--channel must not be empty" >&2
  exit 1
fi

for required_cmd in apt-get; do
  if ! command -v "${required_cmd}" >/dev/null 2>&1; then
    echo "Missing required command: ${required_cmd}" >&2
    exit 1
  fi
done

if ! command -v curl >/dev/null 2>&1 || ! command -v gpg >/dev/null 2>&1; then
  apt-get update
  apt-get install -y curl gnupg ca-certificates
fi

repo_url="${BASE_URL%/}/${CHANNEL}"
key_url="${repo_url}/loramapr-archive-keyring.asc"

echo "Configuring LoRaMapr Receiver APT source"
echo "  channel: ${CHANNEL}"
echo "  repo:    ${repo_url}"

tmp_dir="$(mktemp -d "${TMPDIR:-/tmp}/loramapr-bootstrap-XXXXXX")"
trap 'rm -rf "${tmp_dir}"' EXIT

curl -fsSL "${key_url}" -o "${tmp_dir}/loramapr-archive-keyring.asc"
gpg --dearmor < "${tmp_dir}/loramapr-archive-keyring.asc" > "${tmp_dir}/loramapr-archive-keyring.gpg"

install -d -m 0755 "$(dirname "${KEYRING_PATH}")"
install -m 0644 "${tmp_dir}/loramapr-archive-keyring.gpg" "${KEYRING_PATH}"

cat > "${LIST_PATH}" <<EOF
deb [signed-by=${KEYRING_PATH}] ${repo_url} ${CHANNEL} main
EOF

apt-get update
apt-get install -y "${PACKAGE_NAME}"

if command -v systemctl >/dev/null 2>&1 && [[ -d /run/systemd/system ]]; then
  systemctl daemon-reload || true
  systemctl enable --now loramapr-receiverd.service || true
fi

echo ""
echo "LoRaMapr Receiver install complete."
echo "Open local portal:"
echo "  http://loramapr-receiver.local:8080"
echo "  or http://<device-lan-ip>:8080"
echo "Then enter pairing code from LoRaMapr Cloud."
