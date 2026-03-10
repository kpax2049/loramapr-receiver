#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
VERSION="${1:-${VERSION:-}}"
GO_BIN="${GO_BIN:-$(command -v go || true)}"

if [[ -z "${GO_BIN}" && -x "/usr/local/go/bin/go" ]]; then
  GO_BIN="/usr/local/go/bin/go"
fi

if [[ -z "${GO_BIN}" ]]; then
  echo "go binary not found. Set GO_BIN or add go to PATH." >&2
  exit 1
fi

if [[ -z "${VERSION}" ]]; then
  echo "Usage: $0 <version>  (or set VERSION env var)" >&2
  exit 1
fi

DIST_DIR="${ROOT_DIR}/dist/${VERSION}"
BUILD_DIR="${DIST_DIR}/build"
ARTIFACTS_DIR="${DIST_DIR}/artifacts"

rm -rf "${DIST_DIR}"
mkdir -p "${BUILD_DIR}" "${ARTIFACTS_DIR}"

targets=(
  "linux amd64 '' tar.gz"
  "linux arm64 '' tar.gz"
  "linux arm 7 tar.gz"
  "darwin amd64 '' tar.gz"
  "darwin arm64 '' tar.gz"
  "windows amd64 '' zip"
)

for target in "${targets[@]}"; do
  # shellcheck disable=SC2086
  set -- ${target}
  GOOS_TARGET="$1"
  GOARCH_TARGET="$2"
  GOARM_TARGET="$3"
  ARCHIVE_KIND="$4"

  ARCH_LABEL="${GOARCH_TARGET}"
  if [[ "${GOARCH_TARGET}" == "arm" && "${GOARM_TARGET}" == "7" ]]; then
    ARCH_LABEL="armv7"
  fi

  TARGET_ID="${GOOS_TARGET}-${ARCH_LABEL}"
  BIN_NAME="loramapr-receiverd"
  if [[ "${GOOS_TARGET}" == "windows" ]]; then
    BIN_NAME="${BIN_NAME}.exe"
  fi

  TARGET_BUILD_DIR="${BUILD_DIR}/${TARGET_ID}"
  mkdir -p "${TARGET_BUILD_DIR}"
  BIN_PATH="${TARGET_BUILD_DIR}/${BIN_NAME}"

  echo "Building ${TARGET_ID}"
  if [[ -n "${GOARM_TARGET}" && "${GOARCH_TARGET}" == "arm" ]]; then
    GOOS="${GOOS_TARGET}" GOARCH="${GOARCH_TARGET}" GOARM="${GOARM_TARGET}" \
      CGO_ENABLED=0 "${GO_BIN}" build -trimpath -ldflags "-s -w" -o "${BIN_PATH}" ./cmd/loramapr-receiverd
  else
    GOOS="${GOOS_TARGET}" GOARCH="${GOARCH_TARGET}" CGO_ENABLED=0 \
      "${GO_BIN}" build -trimpath -ldflags "-s -w" -o "${BIN_PATH}" ./cmd/loramapr-receiverd
  fi

  ARTIFACT_BASE="loramapr-receiver_${VERSION}_${GOOS_TARGET}_${ARCH_LABEL}"

  if [[ "${ARCHIVE_KIND}" == "zip" ]]; then
    (
      cd "${TARGET_BUILD_DIR}"
      zip -q -r "${ARTIFACTS_DIR}/${ARTIFACT_BASE}.zip" "${BIN_NAME}"
    )
  else
    (
      cd "${TARGET_BUILD_DIR}"
      tar -czf "${ARTIFACTS_DIR}/${ARTIFACT_BASE}.tar.gz" "${BIN_NAME}"
    )
  fi

  if [[ "${GOOS_TARGET}" == "linux" ]]; then
    LAYOUT_BASE="loramapr-receiver_${VERSION}_${GOOS_TARGET}_${ARCH_LABEL}_systemd"
    STAGE_DIR="${TARGET_BUILD_DIR}/layout"
    mkdir -p \
      "${STAGE_DIR}/usr/bin" \
      "${STAGE_DIR}/etc/loramapr" \
      "${STAGE_DIR}/etc/systemd/system" \
      "${STAGE_DIR}/usr/share/loramapr/scripts"

    cp "${BIN_PATH}" "${STAGE_DIR}/usr/bin/loramapr-receiverd"
    cp "${ROOT_DIR}/packaging/linux/systemd/loramapr-receiverd.service" "${STAGE_DIR}/etc/systemd/system/loramapr-receiverd.service"
    cp "${ROOT_DIR}/receiver.example.json" "${STAGE_DIR}/etc/loramapr/receiver.json"
    cp "${ROOT_DIR}/packaging/linux/scripts/install.sh" "${STAGE_DIR}/usr/share/loramapr/scripts/install.sh"
    cp "${ROOT_DIR}/packaging/linux/scripts/uninstall.sh" "${STAGE_DIR}/usr/share/loramapr/scripts/uninstall.sh"

    (
      cd "${STAGE_DIR}"
      tar -czf "${ARTIFACTS_DIR}/${LAYOUT_BASE}.tar.gz" .
    )
  fi
done

(
  cd "${ARTIFACTS_DIR}"
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum * > SHA256SUMS
  else
    shasum -a 256 * > SHA256SUMS
  fi
)

echo "Artifacts generated under ${ARTIFACTS_DIR}"
