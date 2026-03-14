#!/usr/bin/env bash
set -euo pipefail

IMAGE_PATH="${1:-}"
if [[ -z "${IMAGE_PATH}" ]]; then
  echo "Usage: $0 <image-artifact>" >&2
  exit 1
fi

if [[ ! -f "${IMAGE_PATH}" ]]; then
  echo "image artifact not found: ${IMAGE_PATH}" >&2
  exit 1
fi

if [[ "${IMAGE_PATH}" != *.img.xz ]]; then
  echo "unexpected image artifact extension (expected .img.xz): ${IMAGE_PATH}" >&2
  exit 1
fi

if ! command -v xz >/dev/null 2>&1; then
  echo "xz command is required for image validation" >&2
  exit 1
fi

# Validate compressed image integrity.
xz -t "${IMAGE_PATH}"

# Basic sanity check: reject trivially small outputs.
size_bytes="$(wc -c < "${IMAGE_PATH}" | tr -d ' ')"
min_bytes=$((128 * 1024 * 1024))
if [[ "${size_bytes}" -lt "${min_bytes}" ]]; then
  echo "image artifact is unexpectedly small (${size_bytes} bytes): ${IMAGE_PATH}" >&2
  exit 1
fi

# Flash tools require raw disk images to be sector-aligned.
raw_size_bytes="$(xz --robot --list "${IMAGE_PATH}" | awk -F'\t' '$1=="totals" { print $5 }')"
if [[ -z "${raw_size_bytes}" ]]; then
  echo "unable to read uncompressed image size: ${IMAGE_PATH}" >&2
  exit 1
fi
if (( raw_size_bytes % 512 != 0 )); then
  echo "invalid image geometry: uncompressed size ${raw_size_bytes} is not a multiple of 512 bytes (${IMAGE_PATH})" >&2
  exit 1
fi

echo "Validated Pi image artifact: ${IMAGE_PATH}"
