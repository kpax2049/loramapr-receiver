# Release Artifacts and Cloud Manifest Mapping

This document maps receiver release outputs to the cloud-side receiver artifact
manifest consumed by onboarding flows.

## Artifact Generation

Use:

```bash
packaging/release/build-artifacts.sh <version> [channel]
```

Artifacts are generated in `dist/<version>/artifacts/` with a shared
`SHA256SUMS` file.

## Naming Rules

Binary archives:

- `loramapr-receiver_<version>_<os>_<arch>.tar.gz`
- `loramapr-receiver_<version>_windows_amd64.zip`

Linux systemd layout archives:

- `loramapr-receiver_<version>_linux_<arch>_systemd.tar.gz`

Manifest and metadata outputs:

- `cloud-manifest.fragment.json`
- `release-metadata.json`

## Cloud Manifest Fields

When publishing to `loramapr-cloud` receiver artifact catalog, map each artifact:

- `receiverVersion`: `<version>`
- `channel`: `stable` or `beta`
- `platform`: one of `raspberry_pi`, `linux`, `macos`, `windows`
- `arch`: `amd64`, `arm64`, or `armv7`
- `downloadUrl`: hosted URL to artifact file
- `checksumSha256`: sha256 from `SHA256SUMS`
- `signatureUrl`: optional (future signing pipeline)
- `recommended`: policy-controlled flag

Cloud-ready mapping is produced directly by `cloud-manifest.fragment.json`. This
fragment already includes platform/arch/checksum/relative URL for each artifact,
including Raspberry Pi aliases for Linux systemd arm64/armv7 outputs.

## Suggested URL Pattern

Suggested host path convention:

- `https://downloads.loramapr.com/receiver/<channel>/<version>/<artifact-file>`

This is compatible with existing cloud catalog patterns and keeps receiver
versioning explicit in URL path.

The generated `relativeUrl` values in the manifest fragment are based on this
path pattern (`receiver/<channel>/<version>/<artifact-file>`).
