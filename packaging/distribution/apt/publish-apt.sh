#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
VERSION="${1:-${VERSION:-}}"
CHANNEL="${2:-${CHANNEL:-stable}}"
OUTPUT_ROOT="${3:-${OUTPUT_ROOT:-${ROOT_DIR}/dist/published}}"
ARTIFACTS_DIR="${ARTIFACTS_DIR:-${ROOT_DIR}/dist/${VERSION}/artifacts}"
SIGNING_MODE="${SIGNING_MODE:-optional}" # required|optional|none
GPG_KEY_ID="${GPG_KEY_ID:-}"
APT_SUITE="${APT_SUITE:-${CHANNEL}}"
APT_COMPONENT="${APT_COMPONENT:-main}"
BASE_URL="${BASE_URL:-https://downloads.loramapr.com}"

if [[ -z "${VERSION}" ]]; then
  echo "Usage: $0 <version> [channel] [output-root]" >&2
  exit 1
fi

if [[ ! -d "${ARTIFACTS_DIR}" ]]; then
  echo "artifact directory not found: ${ARTIFACTS_DIR}" >&2
  exit 1
fi

if ! command -v dpkg-scanpackages >/dev/null 2>&1; then
  echo "dpkg-scanpackages is required for APT repository generation" >&2
  exit 1
fi

REQUIRED_DEBS=(
  "loramapr-receiver_${VERSION}_linux_amd64.deb"
  "loramapr-receiver_${VERSION}_linux_arm64.deb"
  "loramapr-receiver_${VERSION}_linux_armv7.deb"
)

REPO_ROOT="${OUTPUT_ROOT}/apt/${CHANNEL}"
POOL_DIR="${REPO_ROOT}/pool/${APT_COMPONENT}/l/loramapr-receiver"
DIST_DIR="${REPO_ROOT}/dists/${APT_SUITE}"
mkdir -p "${POOL_DIR}" "${DIST_DIR}"

for deb in "${REQUIRED_DEBS[@]}"; do
  if [[ ! -f "${ARTIFACTS_DIR}/${deb}" ]]; then
    echo "required deb artifact missing: ${ARTIFACTS_DIR}/${deb}" >&2
    exit 1
  fi
  cp -f "${ARTIFACTS_DIR}/${deb}" "${POOL_DIR}/${deb}"
done

archs=(amd64 arm64 armhf)
for arch in "${archs[@]}"; do
  bin_dir="${DIST_DIR}/${APT_COMPONENT}/binary-${arch}"
  mkdir -p "${bin_dir}"
  (
    cd "${REPO_ROOT}"
    dpkg-scanpackages -a "${arch}" "pool/${APT_COMPONENT}" /dev/null > "${bin_dir#${REPO_ROOT}/}/Packages"
  )
  gzip -n -9 -c "${bin_dir}/Packages" > "${bin_dir}/Packages.gz"
done

release_file="${DIST_DIR}/Release"
{
  echo "Origin: LoRaMapr"
  echo "Label: LoRaMapr Receiver"
  echo "Suite: ${APT_SUITE}"
  echo "Codename: ${APT_SUITE}"
  echo "Date: $(LC_ALL=C date -u '+%a, %d %b %Y %H:%M:%S UTC')"
  echo "Architectures: amd64 arm64 armhf"
  echo "Components: ${APT_COMPONENT}"
  echo "Description: LoRaMapr Receiver APT Repository"
  echo "MD5Sum:"
  while IFS= read -r rel; do
    md5="$(md5sum "${DIST_DIR}/${rel}" | awk '{print $1}')"
    size="$(wc -c < "${DIST_DIR}/${rel}" | tr -d ' ')"
    printf " %s %16s %s\n" "${md5}" "${size}" "${rel}"
  done < <(find "${DIST_DIR}" -type f \( -name "Packages" -o -name "Packages.gz" \) -print | sed "s#^${DIST_DIR}/##" | sort)
  echo "SHA256:"
  while IFS= read -r rel; do
    sha="$(sha256sum "${DIST_DIR}/${rel}" | awk '{print $1}')"
    size="$(wc -c < "${DIST_DIR}/${rel}" | tr -d ' ')"
    printf " %s %16s %s\n" "${sha}" "${size}" "${rel}"
  done < <(find "${DIST_DIR}" -type f \( -name "Packages" -o -name "Packages.gz" \) -print | sed "s#^${DIST_DIR}/##" | sort)
} > "${release_file}"

sign_release() {
  rm -f "${DIST_DIR}/InRelease" "${DIST_DIR}/Release.gpg"

  if ! command -v gpg >/dev/null 2>&1; then
    if [[ "${SIGNING_MODE}" == "required" ]]; then
      echo "gpg is required but not installed" >&2
      exit 1
    fi
    echo "gpg not found; continuing without apt signatures (SIGNING_MODE=${SIGNING_MODE})"
    return
  fi
  if [[ -z "${GPG_KEY_ID}" ]]; then
    if [[ "${SIGNING_MODE}" == "required" ]]; then
      echo "GPG_KEY_ID is required when SIGNING_MODE=required" >&2
      exit 1
    fi
    echo "GPG_KEY_ID not set; continuing without apt signatures (SIGNING_MODE=${SIGNING_MODE})"
    return
  fi

  gpg --batch --yes --local-user "${GPG_KEY_ID}" --detach-sign -o "${DIST_DIR}/Release.gpg" "${release_file}"
  gpg --batch --yes --local-user "${GPG_KEY_ID}" --clearsign -o "${DIST_DIR}/InRelease" "${release_file}"
  gpg --batch --yes --armor --export "${GPG_KEY_ID}" > "${REPO_ROOT}/loramapr-archive-keyring.asc"
  gpg --batch --yes --export "${GPG_KEY_ID}" > "${REPO_ROOT}/loramapr-archive-keyring.gpg"
}

if [[ "${SIGNING_MODE}" != "none" ]]; then
  sign_release
else
  rm -f "${DIST_DIR}/InRelease" "${DIST_DIR}/Release.gpg"
fi

APT_SUMMARY="${REPO_ROOT}/apt-summary.json"
cat > "${APT_SUMMARY}" <<JSON
{
  "receiverVersion": "${VERSION}",
  "channel": "${CHANNEL}",
  "suite": "${APT_SUITE}",
  "component": "${APT_COMPONENT}",
  "architectures": ["amd64", "arm64", "armhf"],
  "repositoryRoot": "${BASE_URL}/apt/${CHANNEL}",
  "debianSourceLine": "deb [signed-by=/usr/share/keyrings/loramapr-archive-keyring.gpg] ${BASE_URL}/apt/${CHANNEL} ${APT_SUITE} ${APT_COMPONENT}",
  "publicKeyAsc": "${BASE_URL}/apt/${CHANNEL}/loramapr-archive-keyring.asc"
}
JSON

echo "APT repository published under ${REPO_ROOT}"
