#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
VERSION="v3.4.0"
CHANNEL="stable"
WORK_DIR="$(mktemp -d "${TMPDIR:-/tmp}/loramapr-apt-publication-test-XXXXXX")"
trap 'rm -rf "${WORK_DIR}"' EXIT

for tool in dpkg-deb dpkg-scanpackages gzip; do
  command -v "${tool}" >/dev/null 2>&1 || {
    echo "${tool} is required" >&2
    exit 1
  }
done

ARTIFACTS_DIR="${WORK_DIR}/artifacts"
PUBLISHED_ROOT="${WORK_DIR}/published"
mkdir -p "${ARTIFACTS_DIR}"

build_test_deb() {
  local release_arch="$1"
  local deb_arch="$2"
  local stage_dir="${WORK_DIR}/stage-${release_arch}"
  mkdir -p "${stage_dir}/DEBIAN"
  cat > "${stage_dir}/DEBIAN/control" <<EOF
Package: loramapr-receiver
Version: 3.4.0
Architecture: ${deb_arch}
Maintainer: LoRaMapr Tests <tests@loramapr.com>
Description: APT publication fixture
EOF
  dpkg-deb --root-owner-group --build "${stage_dir}" \
    "${ARTIFACTS_DIR}/loramapr-receiver_${VERSION}_linux_${release_arch}.deb" >/dev/null
}

build_test_deb amd64 amd64
build_test_deb arm64 arm64
build_test_deb armv7 armhf

ARTIFACTS_DIR="${ARTIFACTS_DIR}" SIGNING_MODE=none \
  "${ROOT_DIR}/packaging/distribution/apt/publish-apt.sh" "${VERSION}" "${CHANNEL}" "${PUBLISHED_ROOT}"

VERSION="${VERSION}" SIGNING_REQUIRED=0 \
  "${ROOT_DIR}/packaging/distribution/apt/verify-apt.sh" "${CHANNEL}" "${PUBLISHED_ROOT}" "${VERSION}"

test -s "${PUBLISHED_ROOT}/apt/${CHANNEL}/dists/${CHANNEL}/main/binary-armhf/Packages"
cmp \
  "${ARTIFACTS_DIR}/loramapr-receiver_${VERSION}_linux_armv7.deb" \
  "${PUBLISHED_ROOT}/apt/${CHANNEL}/pool/main/l/loramapr-receiver/loramapr-receiver_${VERSION}_linux_armhf.deb"

armhf_packages="${PUBLISHED_ROOT}/apt/${CHANNEL}/dists/${CHANNEL}/main/binary-armhf/Packages"
armhf_packages_gz="${armhf_packages}.gz"
: > "${armhf_packages}"
gzip -n -9 -c "${armhf_packages}" > "${armhf_packages_gz}"
if VERSION="${VERSION}" SIGNING_REQUIRED=0 \
  "${ROOT_DIR}/packaging/distribution/apt/verify-apt.sh" "${CHANNEL}" "${PUBLISHED_ROOT}" "${VERSION}"; then
  echo "verify-apt accepted an empty armhf Packages index" >&2
  exit 1
fi

echo "APT publication regression checks passed"
