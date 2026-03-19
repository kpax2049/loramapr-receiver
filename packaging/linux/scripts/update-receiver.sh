#!/usr/bin/env bash
set -euo pipefail

PACKAGE_NAME="${PACKAGE_NAME:-loramapr-receiver}"
SERVICE_NAME="${SERVICE_NAME:-loramapr-receiverd.service}"
CONFIG_PATH="${CONFIG_PATH:-/etc/loramapr/receiver.json}"
STATE_PATH="${STATE_PATH:-/var/lib/loramapr/receiver-state.json}"
BACKUP_ROOT="${BACKUP_ROOT:-/var/backups/loramapr}"

if [[ "$(id -u)" -ne 0 ]]; then
  echo "This script must run as root. Re-run with sudo." >&2
  exit 1
fi

if ! command -v apt-get >/dev/null 2>&1; then
  echo "apt-get not found; this helper supports Debian-family systems only." >&2
  exit 1
fi

timestamp="$(date -u +%Y%m%dT%H%M%SZ)"
backup_dir="${BACKUP_ROOT}/receiver-upgrade-${timestamp}"
mkdir -p "${backup_dir}"

copy_if_exists() {
  src="$1"
  if [[ -f "${src}" ]]; then
    cp -a "${src}" "${backup_dir}/"
  fi
}

copy_if_exists "${CONFIG_PATH}"
copy_if_exists "${STATE_PATH}"

installed="false"
if dpkg-query -W -f='${Status}' "${PACKAGE_NAME}" 2>/dev/null | grep -q "install ok installed"; then
  installed="true"
fi

echo "Updating APT package index..."
apt-get update

install_args=(
  install
  -y
  -o Dpkg::Options::=--force-confold
  "${PACKAGE_NAME}"
)
if [[ "${installed}" == "true" ]]; then
  install_args=(
    install
    -y
    -o Dpkg::Options::=--force-confold
    --only-upgrade
    "${PACKAGE_NAME}"
  )
fi

echo "Installing ${PACKAGE_NAME} (non-interactive, keep local config)..."
APT_LISTCHANGES_FRONTEND=none DEBIAN_FRONTEND=noninteractive apt-get "${install_args[@]}"

if command -v systemctl >/dev/null 2>&1 && [[ -d /run/systemd/system ]]; then
  systemctl daemon-reload || true
  systemctl enable --now "${SERVICE_NAME}" || true
fi

version="$(dpkg-query -W -f='${Version}' "${PACKAGE_NAME}" 2>/dev/null || true)"
echo "LoRaMapr Receiver upgrade complete."
if [[ -n "${version}" ]]; then
  echo "  installed version: ${version}"
fi
echo "  config/state backup: ${backup_dir}"
