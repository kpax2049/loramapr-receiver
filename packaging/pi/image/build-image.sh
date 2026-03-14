#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "${SCRIPT_DIR}/../../.." && pwd)"
VERSION="${1:-${VERSION:-}}"
CHANNEL="${2:-${CHANNEL:-stable}}"
PI_GEN_DIR="${PI_GEN_DIR:-}"
PI_GEN_DEPLOY_DIR="${PI_GEN_DEPLOY_DIR:-}"
PI_GEN_BUILD_CMD="${PI_GEN_BUILD_CMD:-./build-docker.sh -c loramapr.config}"
PI_IMAGE_PREP_ONLY="${PI_IMAGE_PREP_ONLY:-0}"
PI_IMAGE_OUTPUT_DIR="${PI_IMAGE_OUTPUT_DIR:-${ROOT_DIR}/dist/${VERSION}/artifacts}"
PI_IMAGE_ARTIFACT_NAME="${PI_IMAGE_ARTIFACT_NAME:-loramapr-receiver_${VERSION}_pi_arm64.img.xz}"
PI_FIRST_USER_NAME="${PI_FIRST_USER_NAME:-loramapr}"
PI_FIRST_USER_PASS="${PI_FIRST_USER_PASS:-loramapr}"

usage() {
  cat <<USAGE
Usage: $0 <version> [channel]

Environment:
  PI_GEN_DIR                    Path to local pi-gen checkout (required)
  PI_GEN_DEPLOY_DIR             Optional override for pi-gen deploy directory
  PI_GEN_BUILD_CMD              Build command run in PI_GEN_DIR
                                (default: ./build-docker.sh -c loramapr.config)
  LORAMAPR_ARM64_SYSTEMD_TARBALL
                                Optional override for arm64 systemd artifact path
  PI_IMAGE_PREP_ONLY            Set to 1 to only prepare stage/config and skip build
  PI_IMAGE_OUTPUT_DIR           Output directory for final image artifact
  PI_IMAGE_ARTIFACT_NAME        Final image artifact filename
  PI_FIRST_USER_NAME            First boot user name in image (default: loramapr)
  PI_FIRST_USER_PASS            First boot user password in image (default: loramapr)
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

if [[ -z "${PI_GEN_DEPLOY_DIR}" ]]; then
  PI_GEN_DEPLOY_DIR="${PI_GEN_DIR}/deploy"
fi

ARTIFACT_DEFAULT="${ROOT_DIR}/dist/${VERSION}/artifacts/loramapr-receiver_${VERSION}_linux_arm64_systemd.tar.gz"
ARTIFACT_PATH="${LORAMAPR_ARM64_SYSTEMD_TARBALL:-${ARTIFACT_DEFAULT}}"
if [[ ! -f "${ARTIFACT_PATH}" ]]; then
  echo "arm64 systemd artifact not found: ${ARTIFACT_PATH}" >&2
  exit 1
fi

STAGE_TEMPLATE="${SCRIPT_DIR}/stage-loramapr"
STAGE_DIR="${PI_GEN_DIR}/stage-loramapr"
IMG_NAME="loramapr-receiver-${VERSION}"

rm -rf "${STAGE_DIR}"
cp -R "${STAGE_TEMPLATE}" "${STAGE_DIR}"
cp "${ARTIFACT_PATH}" "${STAGE_DIR}/files/loramapr-receiver_systemd.tar.gz"
cp "${ROOT_DIR}/packaging/pi/receiver.appliance.json" "${STAGE_DIR}/files/receiver.appliance.json"

cat > "${PI_GEN_DIR}/loramapr.config" <<CONFIG
IMG_NAME='${IMG_NAME}'
ARCH=arm64
DEPLOY_COMPRESSION=none
STAGE_LIST="stage0 stage1 stage2 stage-loramapr"
FIRST_USER_NAME='${PI_FIRST_USER_NAME}'
FIRST_USER_PASS='${PI_FIRST_USER_PASS}'
DISABLE_FIRST_BOOT_USER_RENAME=1
CONFIG

# Some pi-gen revisions hardcode ARCH=armhf after loading config. Patch that line
# so ARCH from loramapr.config can be honored deterministically in CI.
PI_GEN_BUILD_SH="${PI_GEN_DIR}/build.sh"
if [[ -f "${PI_GEN_BUILD_SH}" ]] && grep -q '^export ARCH=armhf$' "${PI_GEN_BUILD_SH}"; then
  tmp_build_sh="$(mktemp "${TMPDIR:-/tmp}/loramapr-pi-buildsh-XXXXXX")"
  sed 's/^export ARCH=armhf$/export ARCH=${ARCH:-armhf}/' "${PI_GEN_BUILD_SH}" > "${tmp_build_sh}"
  mv "${tmp_build_sh}" "${PI_GEN_BUILD_SH}"
  chmod +x "${PI_GEN_BUILD_SH}"
fi

if [[ "${PI_IMAGE_PREP_ONLY}" == "1" ]]; then
  echo "Prepared pi-gen stage: ${STAGE_DIR}"
  echo "Prepared pi-gen config: ${PI_GEN_DIR}/loramapr.config"
  echo "Prep-only mode enabled (PI_IMAGE_PREP_ONLY=1); skipping image build."
  exit 0
fi

(
  cd "${PI_GEN_DIR}"
  bash -lc "${PI_GEN_BUILD_CMD}"
)

if [[ ! -d "${PI_GEN_DEPLOY_DIR}" ]]; then
  echo "pi-gen deploy directory not found after build: ${PI_GEN_DEPLOY_DIR}" >&2
  exit 1
fi

find_latest_image() {
  local pattern="$1"
  find "${PI_GEN_DEPLOY_DIR}" -maxdepth 1 -type f -name "${pattern}" -print | sort | tail -n 1
}

SOURCE_IMAGE_XZ="$(find_latest_image "*${IMG_NAME}*.img.xz")"
SOURCE_IMAGE_RAW="$(find_latest_image "*${IMG_NAME}*.img")"

if [[ -z "${SOURCE_IMAGE_XZ}" && -z "${SOURCE_IMAGE_RAW}" ]]; then
  echo "no pi image output found in ${PI_GEN_DEPLOY_DIR} for ${IMG_NAME}" >&2
  exit 1
fi

WORK_DIR="$(mktemp -d "${TMPDIR:-/tmp}/loramapr-pi-image-${VERSION}-XXXXXX")"
trap 'rm -rf "${WORK_DIR}"' EXIT

if [[ -n "${SOURCE_IMAGE_XZ}" ]]; then
  NORMALIZED_IMAGE="${SOURCE_IMAGE_XZ}"
else
  if ! command -v xz >/dev/null 2>&1; then
    echo "xz is required to compress .img output to .img.xz artifact" >&2
    exit 1
  fi
  NORMALIZED_IMAGE="${WORK_DIR}/$(basename "${SOURCE_IMAGE_RAW}").xz"
  cp "${SOURCE_IMAGE_RAW}" "${WORK_DIR}/image.img"
  xz -T0 -6 -c "${WORK_DIR}/image.img" > "${NORMALIZED_IMAGE}"
fi

mkdir -p "${PI_IMAGE_OUTPUT_DIR}"
DEST_IMAGE="${PI_IMAGE_OUTPUT_DIR}/${PI_IMAGE_ARTIFACT_NAME}"
cp "${NORMALIZED_IMAGE}" "${DEST_IMAGE}"

"${SCRIPT_DIR}/validate-image.sh" "${DEST_IMAGE}"

METADATA_PATH="${PI_IMAGE_OUTPUT_DIR}/loramapr-receiver_${VERSION}_pi_arm64.image-metadata.json"
cat > "${METADATA_PATH}" <<JSON
{
  "receiverVersion": "${VERSION}",
  "channel": "${CHANNEL}",
  "platform": "raspberry_pi",
  "arch": "arm64",
  "kind": "appliance_image",
  "format": "img.xz",
  "artifact": "$(basename "${DEST_IMAGE}")",
  "sourceImage": "$(basename "${SOURCE_IMAGE_XZ:-${SOURCE_IMAGE_RAW}}")",
  "piGenDir": "${PI_GEN_DIR}",
  "buildCommand": "${PI_GEN_BUILD_CMD}"
}
JSON

echo "Pi appliance image artifact created: ${DEST_IMAGE}"
echo "Pi image metadata created: ${METADATA_PATH}"
