# Release Artifacts and Download Mapping

This document explains which LoRaMapr Receiver artifacts are published and how
cloud onboarding should reference them.

## Which Download Should I Use?

For end users:

1. Raspberry Pi OS Lite / Debian / Ubuntu existing host:
   - use APT repository install (`loramapr-receiver` package)
   - or manual `.deb` fallback for your architecture

For install guide:

- [Linux/Pi Existing-OS Install Path](./linux-pi-distribution.md)

Receiver appliance image path is currently deprecated/paused and is not part of
active release outputs.

## Artifact Generation

Build release artifacts:

```bash
packaging/release/build-artifacts.sh <version> [channel]
```

Upload artifacts to GitHub release assets:

```bash
packaging/release/publish-github-release-assets.sh <version>
```

Outputs are written to `dist/<version>/artifacts/` with `SHA256SUMS`.

## Naming Conventions

General binary archives:

- `loramapr-receiver_<version>_<os>_<arch>.tar.gz`
- `loramapr-receiver_<version>_windows_amd64.zip`

Linux package outputs:

- `loramapr-receiver_<version>_linux_amd64.deb`
- `loramapr-receiver_<version>_linux_arm64.deb`
- `loramapr-receiver_<version>_linux_armv7.deb`

Linux advanced fallback archives:

- `loramapr-receiver_<version>_linux_<arch>_systemd.tar.gz`

Manifest/metadata outputs:

- `cloud-manifest.fragment.json`
- `release-metadata.json`
- `SHA256SUMS`

Optional signature outputs (when signing enabled):

- detached `*.asc` files for artifacts and metadata

## Cloud Manifest Mapping

Cloud onboarding maps each artifact using:

- `receiverVersion`
- `channel`
- `platform`
- `arch`
- `kind`
- `downloadUrl`
- `checksumSha256`
- optional `signatureUrl`

`cloud-manifest.fragment.json` is the source file for this mapping.

Typical kinds:

- `deb_package`
- `systemd_layout`
- `binary`

## Published URL Pattern

Suggested path pattern:

- `https://downloads.loramapr.com/receiver/<channel>/<version>/<artifact-file>`

This keeps version/channel explicit and aligns with generated manifest
`relativeUrl` values.

## Validation Helpers (Maintainers)

- `packaging/debian/validate-deb.sh <deb-file>`
- `packaging/distribution/verify.sh <version> <channel>`

For signed publication and APT repository details:

- `packaging/distribution/README.md`
- `docs/linux-pi-distribution.md`
