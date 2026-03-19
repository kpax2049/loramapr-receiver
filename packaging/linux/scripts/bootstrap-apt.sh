#!/usr/bin/env bash
set -euo pipefail

CHANNEL="stable"
BASE_URL="https://downloads.loramapr.com/apt"
PACKAGE_NAME="loramapr-receiver"
KEYRING_PATH="/usr/share/keyrings/loramapr-archive-keyring.gpg"
LIST_PATH="/etc/apt/sources.list.d/loramapr-receiver.list"
CLOUD_BASE_URL="${LORAMAPR_CLOUD_BASE_URL:-}"

usage() {
  cat <<'EOF'
Usage:
  bootstrap-apt.sh [--channel stable|beta] [--base-url <apt-root>] [--package <name>] [--cloud-base-url <api-origin>]

Examples:
  sudo ./bootstrap-apt.sh
  sudo ./bootstrap-apt.sh --channel beta
  sudo ./bootstrap-apt.sh --cloud-base-url http://192.168.178.22:3001
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
    --cloud-base-url)
      CLOUD_BASE_URL="${2:-}"
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

if [[ -n "${CLOUD_BASE_URL}" && ! "${CLOUD_BASE_URL}" =~ ^https?:// ]]; then
  echo "--cloud-base-url must start with http:// or https://" >&2
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

if ! curl -fsSL "${key_url}" -o "${tmp_dir}/loramapr-archive-keyring.asc"; then
  echo "Failed to fetch signing key: ${key_url}" >&2
  echo "Verify DNS/HTTPS for downloads.loramapr.com and published channel path." >&2
  exit 1
fi
gpg --dearmor < "${tmp_dir}/loramapr-archive-keyring.asc" > "${tmp_dir}/loramapr-archive-keyring.gpg"

install -d -m 0755 "$(dirname "${KEYRING_PATH}")"
install -m 0644 "${tmp_dir}/loramapr-archive-keyring.gpg" "${KEYRING_PATH}"

cat > "${LIST_PATH}" <<EOF
deb [signed-by=${KEYRING_PATH}] ${repo_url} ${CHANNEL} main
EOF

if ! apt-get update; then
  echo "apt-get update failed for source: ${repo_url}" >&2
  echo "Verify that ${repo_url} is reachable and signed metadata is published." >&2
  exit 1
fi
APT_LISTCHANGES_FRONTEND=none DEBIAN_FRONTEND=noninteractive \
  apt-get install -y -o Dpkg::Options::=--force-confold "${PACKAGE_NAME}"

cloud_updated="false"
if [[ -n "${CLOUD_BASE_URL}" ]]; then
  echo "Configuring receiver cloud endpoint"
  echo "  cloud:   ${CLOUD_BASE_URL}"
  if ! /usr/bin/loramapr-receiverd configure-cloud -config /etc/loramapr/receiver.json -base-url "${CLOUD_BASE_URL}"; then
    echo "Failed to apply cloud base URL to receiver config." >&2
    echo "Use: sudo /usr/bin/loramapr-receiverd configure-cloud -config /etc/loramapr/receiver.json -base-url <url>" >&2
    exit 1
  fi
  chown root:loramapr /etc/loramapr/receiver.json >/dev/null 2>&1 || true
  chmod 0640 /etc/loramapr/receiver.json >/dev/null 2>&1 || true
  cloud_updated="true"
fi

if command -v systemctl >/dev/null 2>&1 && [[ -d /run/systemd/system ]]; then
  systemctl daemon-reload || true
  systemctl enable --now loramapr-receiverd.service || true
  if [[ "${cloud_updated}" == "true" ]]; then
    systemctl restart loramapr-receiverd.service || true
  fi
fi

echo ""
echo "LoRaMapr Receiver install complete."
echo "Open local portal:"
echo "  http://loramapr-receiver.local:8080"
echo "  or http://<device-lan-ip>:8080"
echo "Then enter pairing code from LoRaMapr Cloud."
