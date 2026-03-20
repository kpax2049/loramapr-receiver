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
  echo "dpkg-deb is required for validation" >&2
  exit 1
fi

WORK_DIR="$(mktemp -d "${TMPDIR:-/tmp}/loramapr-validate-deb-XXXXXX")"
trap 'rm -rf "${WORK_DIR}"' EXIT

ROOT_DIR="${WORK_DIR}/root"
CONTROL_DIR="${WORK_DIR}/control"
mkdir -p "${ROOT_DIR}" "${CONTROL_DIR}"

dpkg-deb -x "${DEB_FILE}" "${ROOT_DIR}"
dpkg-deb -e "${DEB_FILE}" "${CONTROL_DIR}"

PACKAGE_NAME="$(dpkg-deb -f "${DEB_FILE}" Package)"
if [[ "${PACKAGE_NAME}" != "loramapr-receiver" ]]; then
  echo "unexpected package name: ${PACKAGE_NAME}" >&2
  exit 1
fi

for required_path in \
  "usr/bin/loramapr-receiverd" \
  "usr/share/loramapr/scripts/update-receiver.sh" \
  "lib/systemd/system/loramapr-receiverd.service" \
  "etc/loramapr/receiver.json"; do
  if [[ ! -f "${ROOT_DIR}/${required_path}" ]]; then
    echo "missing required package file: ${required_path}" >&2
    exit 1
  fi
done

for required_dir in \
  "var/lib/loramapr" \
  "var/log/loramapr"; do
  if [[ ! -d "${ROOT_DIR}/${required_dir}" ]]; then
    echo "missing required package directory: ${required_dir}" >&2
    exit 1
  fi
done

UNIT_PATH="${ROOT_DIR}/lib/systemd/system/loramapr-receiverd.service"
if ! grep -Fq 'User=loramapr' "${UNIT_PATH}"; then
  echo "packaged systemd unit missing service user loramapr" >&2
  exit 1
fi
if ! grep -Fq 'Group=loramapr' "${UNIT_PATH}"; then
  echo "packaged systemd unit missing service group loramapr" >&2
  exit 1
fi
if ! grep -Fq 'SupplementaryGroups=dialout' "${UNIT_PATH}"; then
  echo "packaged systemd unit missing dialout supplementary group" >&2
  exit 1
fi
if ! grep -Fq 'TimeoutStopSec=30' "${UNIT_PATH}"; then
  echo "packaged systemd unit missing TimeoutStopSec hardening" >&2
  exit 1
fi
if ! grep -Fq 'RestartForceExitStatus=SIGHUP' "${UNIT_PATH}"; then
  echo "packaged systemd unit missing SIGHUP restart hardening" >&2
  exit 1
fi

CONFIG_PATH="${ROOT_DIR}/etc/loramapr/receiver.json"
if ! grep -Fq '"state_file": "/var/lib/loramapr/receiver-state.json"' "${CONFIG_PATH}"; then
  echo "packaged config missing production state_file default" >&2
  exit 1
fi
if ! grep -Fq '"bind_address": "0.0.0.0:8080"' "${CONFIG_PATH}"; then
  echo "packaged config missing LAN portal bind default" >&2
  exit 1
fi
if ! grep -Fq '"profile": "linux-service"' "${CONFIG_PATH}"; then
  echo "packaged config missing linux-service runtime profile default" >&2
  exit 1
fi
if ! grep -Fq '"base_url": "https://loramapr.com"' "${CONFIG_PATH}"; then
  echo "packaged config missing production cloud base_url default" >&2
  exit 1
fi
if ! grep -Fq '"transport": "bridge"' "${CONFIG_PATH}"; then
  echo "packaged config missing bridge transport default" >&2
  exit 1
fi

for required_control in postinst prerm postrm conffiles; do
  if [[ ! -f "${CONTROL_DIR}/${required_control}" ]]; then
    echo "missing required control file: ${required_control}" >&2
    exit 1
  fi
done

echo "Validated ${DEB_FILE}"
