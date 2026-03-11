#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
VERSION="${1:-${VERSION:-}}"
CHANNEL="${2:-${CHANNEL:-stable}}"
PUBLISHED_ROOT="${3:-${PUBLISHED_ROOT:-${ROOT_DIR}/dist/published}}"

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

echo "Published distribution verified: ${TARGET_DIR}"
