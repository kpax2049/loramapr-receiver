#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
CHANNEL="${1:-${CHANNEL:-stable}}"
OUTPUT_ROOT="${2:-${OUTPUT_ROOT:-${ROOT_DIR}/dist/published}}"
APT_SUITE="${APT_SUITE:-${CHANNEL}}"
APT_COMPONENT="${APT_COMPONENT:-main}"
SIGNING_REQUIRED="${SIGNING_REQUIRED:-0}"

REPO_ROOT="${OUTPUT_ROOT}/apt/${CHANNEL}"
DIST_DIR="${REPO_ROOT}/dists/${APT_SUITE}"
if [[ ! -d "${DIST_DIR}" ]]; then
  echo "apt dist directory not found: ${DIST_DIR}" >&2
  exit 1
fi

for arch in amd64 arm64 armhf; do
  packages_file="${DIST_DIR}/${APT_COMPONENT}/binary-${arch}/Packages"
  packages_gz="${packages_file}.gz"
  if [[ ! -f "${packages_file}" ]]; then
    echo "missing Packages index: ${packages_file}" >&2
    exit 1
  fi
  if [[ ! -f "${packages_gz}" ]]; then
    echo "missing Packages.gz index: ${packages_gz}" >&2
    exit 1
  fi
  gzip -t "${packages_gz}"
done

if [[ ! -f "${DIST_DIR}/Release" ]]; then
  echo "missing Release metadata: ${DIST_DIR}/Release" >&2
  exit 1
fi

if [[ -f "${DIST_DIR}/InRelease" ]]; then
  if ! command -v gpgv >/dev/null 2>&1; then
    echo "gpgv not installed; cannot verify InRelease signature" >&2
    exit 1
  fi
  if [[ ! -f "${REPO_ROOT}/loramapr-archive-keyring.gpg" ]]; then
    echo "missing apt keyring file for signature verification" >&2
    exit 1
  fi
  gpgv --keyring "${REPO_ROOT}/loramapr-archive-keyring.gpg" "${DIST_DIR}/InRelease" >/dev/null
elif [[ "${SIGNING_REQUIRED}" == "1" ]]; then
  echo "signature required but InRelease was not generated" >&2
  exit 1
fi

echo "APT repository verified: ${REPO_ROOT}"
