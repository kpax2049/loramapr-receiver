#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
VERSION="${VERSION:-${1:-}}"
ARCH_LABEL="${ARCH_LABEL:-${2:-}}"
BIN_PATH="${BIN_PATH:-${3:-}}"
OUT_DIR="${OUT_DIR:-${4:-}}"
PACKAGE_NAME="${PACKAGE_NAME:-loramapr-receiver}"
MAINTAINER="${MAINTAINER:-LoRaMapr Maintainers <maintainers@loramapr.com>}"

usage() {
  cat <<'EOF'
Usage:
  build-deb.sh <version> <arch-label> <binary-path> <out-dir>

Environment overrides:
  VERSION, ARCH_LABEL, BIN_PATH, OUT_DIR, PACKAGE_NAME, MAINTAINER
EOF
}

if [[ -z "${VERSION}" || -z "${ARCH_LABEL}" || -z "${BIN_PATH}" || -z "${OUT_DIR}" ]]; then
  usage >&2
  exit 1
fi

if [[ ! -f "${BIN_PATH}" ]]; then
  echo "binary not found: ${BIN_PATH}" >&2
  exit 1
fi

if ! command -v dpkg-deb >/dev/null 2>&1; then
  echo "dpkg-deb is required to build .deb artifacts" >&2
  exit 1
fi

map_deb_arch() {
  local arch="$1"
  case "${arch}" in
    amd64) echo "amd64" ;;
    arm64) echo "arm64" ;;
    armv7|armhf) echo "armhf" ;;
    *)
      echo "unsupported architecture label for deb build: ${arch}" >&2
      return 1
      ;;
  esac
}

DEB_ARCH="$(map_deb_arch "${ARCH_LABEL}")"
DEB_VERSION="${VERSION}"
ARTIFACT_NAME="loramapr-receiver_${VERSION}_linux_${ARCH_LABEL}.deb"

WORK_DIR="$(mktemp -d "${TMPDIR:-/tmp}/loramapr-receiver-deb-${ARCH_LABEL}-XXXXXX")"
trap 'rm -rf "${WORK_DIR}"' EXIT

STAGE_DIR="${WORK_DIR}/${PACKAGE_NAME}"
DEBIAN_DIR="${STAGE_DIR}/DEBIAN"
mkdir -p \
  "${DEBIAN_DIR}" \
  "${STAGE_DIR}/usr/bin" \
  "${STAGE_DIR}/lib/systemd/system" \
  "${STAGE_DIR}/etc/loramapr" \
  "${STAGE_DIR}/var/lib/loramapr" \
  "${STAGE_DIR}/var/log/loramapr"

install -m 0755 "${BIN_PATH}" "${STAGE_DIR}/usr/bin/loramapr-receiverd"
install -m 0644 "${ROOT_DIR}/packaging/linux/systemd/loramapr-receiverd.service" "${STAGE_DIR}/lib/systemd/system/loramapr-receiverd.service"
install -m 0644 "${ROOT_DIR}/receiver.example.json" "${STAGE_DIR}/etc/loramapr/receiver.json"

cat > "${DEBIAN_DIR}/control" <<EOF
Package: ${PACKAGE_NAME}
Version: ${DEB_VERSION}
Section: net
Priority: optional
Architecture: ${DEB_ARCH}
Maintainer: ${MAINTAINER}
Depends: adduser, systemd
Description: LoRaMapr Receiver runtime service
 Standalone LoRaMapr receiver runtime with embedded setup portal and
 Linux/systemd service integration.
EOF

cat > "${DEBIAN_DIR}/conffiles" <<'EOF'
/etc/loramapr/receiver.json
EOF

install -m 0755 "${ROOT_DIR}/packaging/debian/scripts/postinst" "${DEBIAN_DIR}/postinst"
install -m 0755 "${ROOT_DIR}/packaging/debian/scripts/prerm" "${DEBIAN_DIR}/prerm"
install -m 0755 "${ROOT_DIR}/packaging/debian/scripts/postrm" "${DEBIAN_DIR}/postrm"

if [[ -n "${SOURCE_DATE_EPOCH:-}" ]]; then
  while IFS= read -r -d '' path; do
    touch -h -d "@${SOURCE_DATE_EPOCH}" "${path}"
  done < <(find "${STAGE_DIR}" -print0)
fi

mkdir -p "${OUT_DIR}"
dpkg-deb --root-owner-group --build "${STAGE_DIR}" "${OUT_DIR}/${ARTIFACT_NAME}" >/dev/null
echo "Built ${OUT_DIR}/${ARTIFACT_NAME}"
