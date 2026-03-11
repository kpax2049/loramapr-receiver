#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
VERSION="${1:-${VERSION:-}}"
CHANNEL="${2:-${CHANNEL:-stable}}"
PUBLISHED_ROOT="${3:-${PUBLISHED_ROOT:-${ROOT_DIR}/dist/published}}"
ENABLE_APT="${ENABLE_APT:-1}"
APT_SUITE="${APT_SUITE:-${CHANNEL}}"
SIGNING_REQUIRED="${SIGNING_REQUIRED:-0}"
PI_IMAGE_REQUIRED="${PI_IMAGE_REQUIRED:-0}"

if [[ -z "${VERSION}" ]]; then
  echo "Usage: $0 <version> [channel] [published-root]" >&2
  exit 1
fi

TARGET_DIR="${PUBLISHED_ROOT}/receiver/${CHANNEL}/${VERSION}"
if [[ ! -d "${TARGET_DIR}" ]]; then
  echo "published release directory not found: ${TARGET_DIR}" >&2
  exit 1
fi

for required in SHA256SUMS cloud-manifest.fragment.json release-metadata.json publish-summary.json; do
  if [[ ! -f "${TARGET_DIR}/${required}" ]]; then
    echo "missing published file: ${required}" >&2
    exit 1
  fi
done

if command -v sha256sum >/dev/null 2>&1; then
  (cd "${TARGET_DIR}" && sha256sum -c SHA256SUMS)
else
  (cd "${TARGET_DIR}" && shasum -a 256 -c SHA256SUMS)
fi

if [[ ! -f "${PUBLISHED_ROOT}/receiver/${CHANNEL}/channel-index.json" ]]; then
  echo "missing channel index" >&2
  exit 1
fi

if [[ "${ENABLE_APT}" != "0" ]]; then
  APT_SUITE="${APT_SUITE}" SIGNING_REQUIRED="${SIGNING_REQUIRED}" \
    "${ROOT_DIR}/packaging/distribution/apt/verify-apt.sh" "${CHANNEL}" "${PUBLISHED_ROOT}"
fi

PI_IMAGE_PATH="${TARGET_DIR}/loramapr-receiver_${VERSION}_pi_arm64.img.xz"
if [[ -f "${PI_IMAGE_PATH}" ]]; then
  "${ROOT_DIR}/packaging/pi/image/validate-image.sh" "${PI_IMAGE_PATH}"
elif [[ "${PI_IMAGE_REQUIRED}" == "1" ]]; then
  echo "pi image required but not found: ${PI_IMAGE_PATH}" >&2
  exit 1
fi

echo "Published distribution verified: ${TARGET_DIR}"
