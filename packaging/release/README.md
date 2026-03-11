# Release Artifact Pipeline

This directory defines the release artifact skeleton for `loramapr-receiverd`.

## Build Matrix

See `targets.json` for the declared matrix:

- `linux/amd64`
- `linux/arm64`
- `linux/armv7`
- `darwin/amd64`
- `darwin/arm64`
- `windows/amd64`

## Build Command

From repository root:

```bash
packaging/release/build-artifacts.sh v1.0.0
```

Optional channel override:

```bash
packaging/release/build-artifacts.sh v1.0.0 beta
```

Outputs are written to:

- `dist/<version>/build/`
- `dist/<version>/artifacts/`

Receiver binaries are stamped with build metadata via `ldflags`:

- version
- channel
- commit
- optional build date (`BUILD_DATE` or `SOURCE_DATE_EPOCH`)

## Naming Convention

Binary archives:

- `loramapr-receiver_<version>_<os>_<arch>.tar.gz`
- `loramapr-receiver_<version>_windows_amd64.zip`

Linux install-layout archives:

- `loramapr-receiver_<version>_linux_<arch>_systemd.tar.gz`

Checksum file:

- `SHA256SUMS` (sha256 over all files in `artifacts/`)

Cloud-manifest metadata:

- `cloud-manifest.fragment.json` (machine-consumable artifact mapping)
- `release-metadata.json` (build summary for maintainers/publish pipelines)

## Linux Layout Archive

`*_systemd.tar.gz` archives include:

- `usr/bin/loramapr-receiverd`
- `etc/loramapr/receiver.json`
- `etc/systemd/system/loramapr-receiverd.service`
- `usr/share/loramapr/scripts/install.sh`
- `usr/share/loramapr/scripts/uninstall.sh`

This aligns with the Linux-first service/install model.

## Cloud Manifest Fragment

`cloud-manifest.fragment.json` is generated directly from artifact filenames and
`SHA256SUMS` and includes:

- `receiverVersion`
- `channel`
- artifact entries with:
  - `platform`, `arch`
  - `kind` (`binary`, `systemd_layout`)
  - `filename`
  - `checksumSha256`
  - `sizeBytes`
  - `relativeUrl`

Raspberry Pi aliases are emitted for Linux arm64/armv7 systemd layout archives
using `platform = raspberry_pi`.
