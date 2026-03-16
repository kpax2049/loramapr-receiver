#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
VERSION="${1:-${VERSION:-}}"
CHANNEL="${2:-${CHANNEL:-stable}}"
OUTPUT_ROOT="${3:-${OUTPUT_ROOT:-${ROOT_DIR}/dist/published}}"
SIGNING_MODE="${SIGNING_MODE:-optional}" # required|optional|none
GPG_KEY_ID="${GPG_KEY_ID:-}"
BASE_URL="${BASE_URL:-https://downloads.loramapr.com}"
ENABLE_APT="${ENABLE_APT:-1}"
APT_SUITE="${APT_SUITE:-${CHANNEL}}"

if [[ -z "${VERSION}" ]]; then
  echo "Usage: $0 <version> [channel] [output-root]" >&2
  exit 1
fi

ARTIFACTS_DIR="${ARTIFACTS_DIR:-${ROOT_DIR}/dist/${VERSION}/artifacts}"
if [[ ! -d "${ARTIFACTS_DIR}" ]]; then
  echo "artifact directory not found: ${ARTIFACTS_DIR}" >&2
  exit 1
fi

required_files=(
  "SHA256SUMS"
  "cloud-manifest.fragment.json"
  "release-metadata.json"
)
for file in "${required_files[@]}"; do
  if [[ ! -f "${ARTIFACTS_DIR}/${file}" ]]; then
    echo "required release file missing: ${ARTIFACTS_DIR}/${file}" >&2
    exit 1
  fi
done

DEST_DIR="${OUTPUT_ROOT}/receiver/${CHANNEL}/${VERSION}"
CHANNEL_DIR="${OUTPUT_ROOT}/receiver/${CHANNEL}"
mkdir -p "${DEST_DIR}" "${CHANNEL_DIR}"

cp -f "${ARTIFACTS_DIR}"/* "${DEST_DIR}/"

# Pi appliance image publication is deprecated in active public flows.
rm -f \
  "${DEST_DIR}/loramapr-receiver_${VERSION}_pi_arm64.img.xz" \
  "${DEST_DIR}/loramapr-receiver_${VERSION}_pi_arm64.image-metadata.json" \
  "${DEST_DIR}/loramapr-receiver-pi-appliance-${VERSION}.img.xz" \
  "${DEST_DIR}/loramapr-receiver_${VERSION}_pi_arm64.img.xz.asc" \
  "${DEST_DIR}/loramapr-receiver_${VERSION}_pi_arm64.image-metadata.json.asc" \
  "${DEST_DIR}/loramapr-receiver-pi-appliance-${VERSION}.img.xz.asc"

INDEX_PATH="${CHANNEL_DIR}/channel-index.json"
cat > "${INDEX_PATH}" <<JSON
{
  "schemaVersion": "receiver-channel-index/v1",
  "channel": "${CHANNEL}",
  "versions": [
    {
      "receiverVersion": "${VERSION}",
      "manifest": "${VERSION}/cloud-manifest.fragment.json",
      "checksums": "${VERSION}/SHA256SUMS",
      "metadata": "${VERSION}/release-metadata.json"
    }
  ]
}
JSON

sign_file() {
  local file="$1"
  gpg --batch --yes --armor --detach-sign -u "${GPG_KEY_ID}" "${file}"
}

if [[ "${SIGNING_MODE}" != "none" ]]; then
  if ! command -v gpg >/dev/null 2>&1; then
    if [[ "${SIGNING_MODE}" == "required" ]]; then
      echo "gpg is required but not installed" >&2
      exit 1
    fi
    echo "gpg not found; continuing without signatures (SIGNING_MODE=${SIGNING_MODE})"
  elif [[ -z "${GPG_KEY_ID}" ]]; then
    if [[ "${SIGNING_MODE}" == "required" ]]; then
      echo "GPG_KEY_ID is required when SIGNING_MODE=required" >&2
      exit 1
    fi
    echo "GPG_KEY_ID not set; continuing without signatures (SIGNING_MODE=${SIGNING_MODE})"
  else
    sign_file "${DEST_DIR}/SHA256SUMS"
    sign_file "${DEST_DIR}/cloud-manifest.fragment.json"
    sign_file "${DEST_DIR}/release-metadata.json"
    sign_file "${INDEX_PATH}"
  fi
fi

if [[ "${ENABLE_APT}" != "0" ]]; then
  APT_SUITE="${APT_SUITE}" \
  SIGNING_MODE="${SIGNING_MODE}" \
  GPG_KEY_ID="${GPG_KEY_ID}" \
  BASE_URL="${BASE_URL}" \
  ARTIFACTS_DIR="${ARTIFACTS_DIR}" \
  "${ROOT_DIR}/packaging/distribution/apt/publish-apt.sh" "${VERSION}" "${CHANNEL}" "${OUTPUT_ROOT}"
fi

apt_summary_block=""
if [[ "${ENABLE_APT}" != "0" ]]; then
  apt_summary_block=$(cat <<JSON
  ,
  "aptRepositoryUrl": "${BASE_URL}/apt/${CHANNEL}",
  "aptSuite": "${APT_SUITE}",
  "aptInstallSource": "deb [signed-by=/usr/share/keyrings/loramapr-archive-keyring.gpg] ${BASE_URL}/apt/${CHANNEL} ${APT_SUITE} main"
JSON
)
fi

SUMMARY_PATH="${DEST_DIR}/publish-summary.json"
cat > "${SUMMARY_PATH}" <<JSON
{
  "receiverVersion": "${VERSION}",
  "channel": "${CHANNEL}",
  "baseUrl": "${BASE_URL}",
  "manifestUrl": "${BASE_URL}/receiver/${CHANNEL}/${VERSION}/cloud-manifest.fragment.json",
  "checksumsUrl": "${BASE_URL}/receiver/${CHANNEL}/${VERSION}/SHA256SUMS",
  "channelIndexUrl": "${BASE_URL}/receiver/${CHANNEL}/channel-index.json"${apt_summary_block}
}
JSON

if command -v sha256sum >/dev/null 2>&1; then
  (cd "${DEST_DIR}" && sha256sum -c SHA256SUMS)
else
  (cd "${DEST_DIR}" && shasum -a 256 -c SHA256SUMS)
fi

echo "Published receiver artifacts to ${DEST_DIR}"
echo "Channel index: ${INDEX_PATH}"
