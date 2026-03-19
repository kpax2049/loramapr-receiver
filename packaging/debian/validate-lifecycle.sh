#!/usr/bin/env bash
set -euo pipefail

DEB_FILE="${1:-}"
if [[ -z "${DEB_FILE}" ]]; then
  echo "Usage: $0 <path-to-deb>" >&2
  exit 1
fi

if [[ ! -f "${DEB_FILE}" ]]; then
  echo "deb file not found: ${DEB_FILE}" >&2
  exit 1
fi

if ! command -v dpkg-deb >/dev/null 2>&1; then
  echo "dpkg-deb is required for lifecycle validation" >&2
  exit 1
fi

WORK_DIR="$(mktemp -d "${TMPDIR:-/tmp}/loramapr-validate-lifecycle-XXXXXX")"
trap 'rm -rf "${WORK_DIR}"' EXIT

CONTROL_DIR="${WORK_DIR}/control"
mkdir -p "${CONTROL_DIR}"
dpkg-deb -e "${DEB_FILE}" "${CONTROL_DIR}"

require_match() {
  local pattern="$1"
  local file="$2"
  local error_message="$3"
  if ! grep -Eq "${pattern}" "${file}"; then
    echo "${error_message}" >&2
    exit 1
  fi
}

require_match '^/etc/loramapr/receiver\.json$' "${CONTROL_DIR}/conffiles" "missing conffile policy for /etc/loramapr/receiver.json"
require_match 'configure\|reconfigure' "${CONTROL_DIR}/postinst" "postinst missing configure handling"
require_match 'ensure_serial_access' "${CONTROL_DIR}/postinst" "postinst missing serial-access hardening hook"
require_match 'dialout' "${CONTROL_DIR}/postinst" "postinst missing dialout membership handling"
require_match 'ensure_runtime_permissions' "${CONTROL_DIR}/postinst" "postinst missing runtime permission hardening hook"
require_match 'normalize_legacy_systemd_unit' "${CONTROL_DIR}/postinst" "postinst missing tarball-to-package unit normalization hook"
require_match 'remove\|deconfigure' "${CONTROL_DIR}/prerm" "prerm missing remove/deconfigure handling"
require_match 'upgrade\|failed-upgrade' "${CONTROL_DIR}/prerm" "prerm missing upgrade handling"
require_match 'purge' "${CONTROL_DIR}/postrm" "postrm missing purge handling"
require_match '/var/lib/loramapr/receiver-state\.json' "${CONTROL_DIR}/postrm" "postrm missing state purge handling"

echo "Validated lifecycle policy for ${DEB_FILE}"
