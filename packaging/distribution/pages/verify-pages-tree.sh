#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
VERSION="${1:-${VERSION:-}}"
CHANNEL="${2:-${CHANNEL:-stable}}"
PAGES_ROOT="${3:-${PAGES_ROOT:-${ROOT_DIR}/dist/published-pages}}"
EXPECTED_CNAME="${4:-${EXPECTED_CNAME:-downloads.loramapr.com}}"
APT_SUITE="${APT_SUITE:-${CHANNEL}}"
SIGNING_REQUIRED="${SIGNING_REQUIRED:-0}"

if [[ -z "${VERSION}" ]]; then
  echo "Usage: $0 <version> [channel] [pages-root] [expected-cname]" >&2
  exit 1
fi

if [[ ! -d "${PAGES_ROOT}" ]]; then
  echo "pages root not found: ${PAGES_ROOT}" >&2
  exit 1
fi

if [[ ! -d "${PAGES_ROOT}/apt/${CHANNEL}" ]]; then
  echo "missing apt channel path in pages tree: ${PAGES_ROOT}/apt/${CHANNEL}" >&2
  exit 1
fi

if [[ ! -d "${PAGES_ROOT}/receiver/${CHANNEL}/${VERSION}" ]]; then
  echo "missing receiver version path in pages tree: ${PAGES_ROOT}/receiver/${CHANNEL}/${VERSION}" >&2
  exit 1
fi

if [[ ! -f "${PAGES_ROOT}/.nojekyll" ]]; then
  echo "missing .nojekyll in pages tree root" >&2
  exit 1
fi

if [[ -n "${EXPECTED_CNAME}" ]]; then
  if [[ ! -f "${PAGES_ROOT}/CNAME" ]]; then
    echo "missing CNAME in pages tree root" >&2
    exit 1
  fi
  cname_value="$(tr -d '[:space:]' < "${PAGES_ROOT}/CNAME")"
  if [[ "${cname_value}" != "${EXPECTED_CNAME}" ]]; then
    echo "unexpected CNAME value: got=${cname_value} expected=${EXPECTED_CNAME}" >&2
    exit 1
  fi
fi

if [[ ! -f "${PAGES_ROOT}/pages-deploy-summary.json" ]]; then
  echo "missing pages summary: ${PAGES_ROOT}/pages-deploy-summary.json" >&2
  exit 1
fi

APT_SUITE="${APT_SUITE}" SIGNING_REQUIRED="${SIGNING_REQUIRED}" \
  "${ROOT_DIR}/packaging/distribution/verify.sh" "${VERSION}" "${CHANNEL}" "${PAGES_ROOT}"

echo "Pages tree verified: ${PAGES_ROOT}"
