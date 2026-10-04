#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
CHANNEL="${1:-${CHANNEL:-stable}}"
OUTPUT_ROOT="${2:-${OUTPUT_ROOT:-${ROOT_DIR}/dist/published}}"
VERSION="${3:-${VERSION:-}}"
APT_SUITE="${APT_SUITE:-${CHANNEL}}"
APT_COMPONENT="${APT_COMPONENT:-main}"
SIGNING_REQUIRED="${SIGNING_REQUIRED:-0}"

if [[ -z "${VERSION}" ]]; then
  echo "Usage: VERSION=<version> $0 <channel> [output-root] [version]" >&2
  exit 1
fi

to_debian_version() {
  local normalized="$1"
  normalized="${normalized#v}"
  normalized="${normalized#V}"
  normalized="${normalized//-/~}"
  normalized="$(printf '%s' "${normalized}" | sed -E 's/[^0-9A-Za-z.+~:]/./g')"
  if [[ ! "${normalized}" =~ ^[0-9] ]]; then
    normalized="0~${normalized}"
  fi
  printf '%s' "${normalized}"
}

EXPECTED_DEB_VERSION="$(to_debian_version "${VERSION}")"

REPO_ROOT="${OUTPUT_ROOT}/apt/${CHANNEL}"
DIST_DIR="${REPO_ROOT}/dists/${APT_SUITE}"
if [[ ! -d "${DIST_DIR}" ]]; then
  echo "apt dist directory not found: ${DIST_DIR}" >&2
  exit 1
fi

verify_packages_index() {
  local packages_file="$1"
  local expected_arch="$2"
  if [[ ! -s "${packages_file}" ]]; then
    echo "empty Packages index: ${packages_file}" >&2
    return 1
  fi
  if ! awk -v expected_version="${EXPECTED_DEB_VERSION}" -v expected_arch="${expected_arch}" '
    BEGIN { RS=""; FS="\n" }
    {
      package=""; version=""; architecture=""
      for (i = 1; i <= NF; i++) {
        if ($i ~ /^Package: /) package = substr($i, 10)
        if ($i ~ /^Version: /) version = substr($i, 10)
        if ($i ~ /^Architecture: /) architecture = substr($i, 15)
      }
      if (package == "loramapr-receiver" && version == expected_version && architecture == expected_arch) found = 1
    }
    END { exit(found ? 0 : 1) }
  ' "${packages_file}"; then
    echo "Packages index missing loramapr-receiver ${EXPECTED_DEB_VERSION} for ${expected_arch}: ${packages_file}" >&2
    return 1
  fi
}

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
  verify_packages_index "${packages_file}" "${arch}"
  expanded_packages="$(mktemp)"
  gzip -dc "${packages_gz}" > "${expanded_packages}"
  if ! cmp -s "${packages_file}" "${expanded_packages}"; then
    rm -f "${expanded_packages}"
    echo "Packages.gz does not match Packages: ${packages_gz}" >&2
    exit 1
  fi
  verify_packages_index "${expanded_packages}" "${arch}"
  rm -f "${expanded_packages}"
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
