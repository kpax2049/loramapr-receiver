#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
VERSION="${1:-${VERSION:-}}"
ARTIFACTS_DIR="${2:-${ARTIFACTS_DIR:-${ROOT_DIR}/dist/${VERSION}/artifacts}}"
REPO="${3:-${GITHUB_REPOSITORY:-}}"

usage() {
  cat <<'EOF'
Usage:
  publish-github-release-assets.sh <version> [artifacts-dir] [repo]

Examples:
  packaging/release/publish-github-release-assets.sh v2.12.0
  packaging/release/publish-github-release-assets.sh v2.12.0 dist/v2.12.0/artifacts kpax2049/loramapr-receiver

Optional environment:
  RELEASE_NOTES_FILE=<path>  Use explicit release notes file when creating a new
                             GitHub release (otherwise --generate-notes is used).
EOF
}

if [[ -z "${VERSION}" || "${VERSION}" == "-h" || "${VERSION}" == "--help" ]]; then
  usage >&2
  exit 1
fi

if [[ -z "${REPO}" ]]; then
  echo "repo is required (third argument or GITHUB_REPOSITORY env)." >&2
  exit 1
fi

if [[ ! -d "${ARTIFACTS_DIR}" ]]; then
  echo "artifact directory not found: ${ARTIFACTS_DIR}" >&2
  exit 1
fi

if ! command -v gh >/dev/null 2>&1; then
  echo "gh CLI is required to publish release assets." >&2
  exit 1
fi

mapfile -t files < <(find "${ARTIFACTS_DIR}" -maxdepth 1 -type f -print | sort)
if [[ "${#files[@]}" -eq 0 ]]; then
  echo "no artifact files found in ${ARTIFACTS_DIR}" >&2
  exit 1
fi

include_deprecated_pi_image="${INCLUDE_DEPRECATED_PI_IMAGE:-0}"
if [[ "${include_deprecated_pi_image}" != "1" ]]; then
  filtered=()
  for file in "${files[@]}"; do
    base="$(basename "${file}")"
    case "${base}" in
      *_pi_arm64.img.xz|*_pi_arm64.image-metadata.json|*-pi-appliance-*.img.xz)
        continue
        ;;
      *)
        filtered+=("${file}")
        ;;
    esac
  done
  files=("${filtered[@]}")
fi

if [[ "${#files[@]}" -eq 0 ]]; then
  echo "no publishable artifact files found in ${ARTIFACTS_DIR}" >&2
  exit 1
fi

if ! gh release view "${VERSION}" --repo "${REPO}" >/dev/null 2>&1; then
  release_create_args=(
    --repo "${REPO}"
    --title "${VERSION}"
  )
  if [[ -n "${RELEASE_NOTES_FILE:-}" ]]; then
    if [[ ! -f "${RELEASE_NOTES_FILE}" ]]; then
      echo "release notes file not found: ${RELEASE_NOTES_FILE}" >&2
      exit 1
    fi
    release_create_args+=(--notes-file "${RELEASE_NOTES_FILE}")
  else
    release_create_args+=(--generate-notes)
  fi
  gh release create "${VERSION}" "${release_create_args[@]}"
fi

gh release upload "${VERSION}" "${files[@]}" --repo "${REPO}" --clobber
echo "Uploaded ${#files[@]} assets to release ${VERSION} (${REPO})."
