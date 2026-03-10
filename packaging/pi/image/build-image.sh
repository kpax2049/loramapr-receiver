#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "${SCRIPT_DIR}/../../.." && pwd)"
VERSION="${1:-${VERSION:-}}"
PI_GEN_DIR="${PI_GEN_DIR:-}"

usage() {
  cat <<USAGE
Usage: $0 <version>

Environment:
  PI_GEN_DIR                    Path to local pi-gen checkout (required)
  LORAMAPR_ARM64_SYSTEMD_TARBALL
                                Optional override for arm64 systemd artifact path
USAGE
}

if [[ "${VERSION}" == "-h" || "${VERSION}" == "--help" || -z "${VERSION}" ]]; then
  usage
  if [[ -z "${VERSION}" ]]; then
    exit 1
  fi
  exit 0
fi

if [[ -z "${PI_GEN_DIR}" ]]; then
  echo "PI_GEN_DIR is required and must point to a pi-gen checkout." >&2
  exit 1
fi

if [[ ! -d "${PI_GEN_DIR}" ]]; then
  echo "pi-gen directory not found: ${PI_GEN_DIR}" >&2
  exit 1
fi

ARTIFACT_DEFAULT="${ROOT_DIR}/dist/${VERSION}/artifacts/loramapr-receiver_${VERSION}_linux_arm64_systemd.tar.gz"
ARTIFACT_PATH="${LORAMAPR_ARM64_SYSTEMD_TARBALL:-${ARTIFACT_DEFAULT}}"

if [[ ! -f "${ARTIFACT_PATH}" ]]; then
  echo "arm64 systemd artifact not found: ${ARTIFACT_PATH}" >&2
  exit 1
fi

STAGE_TEMPLATE="${SCRIPT_DIR}/stage-loramapr"
STAGE_DIR="${PI_GEN_DIR}/stage-loramapr"

rm -rf "${STAGE_DIR}"
cp -R "${STAGE_TEMPLATE}" "${STAGE_DIR}"
cp "${ARTIFACT_PATH}" "${STAGE_DIR}/files/loramapr-receiver_systemd.tar.gz"
cp "${ROOT_DIR}/packaging/pi/receiver.appliance.json" "${STAGE_DIR}/files/receiver.appliance.json"

cat > "${PI_GEN_DIR}/loramapr.config" <<CONFIG
IMG_NAME='loramapr-receiver-${VERSION}'
DEPLOY_ZIP=0
STAGE_LIST="stage0 stage1 stage2 stage-loramapr"
CONFIG

echo "Prepared pi-gen stage: ${STAGE_DIR}"
echo "Prepared pi-gen config: ${PI_GEN_DIR}/loramapr.config"
echo "Next step: cd ${PI_GEN_DIR} && ./build-docker.sh -c loramapr.config"
