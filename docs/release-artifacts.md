# Release Artifacts and Cloud Manifest Mapping

This document maps receiver release outputs to the cloud-side receiver artifact
manifest consumed by onboarding flows.

## Artifact Generation

Use:

```bash
packaging/release/build-artifacts.sh <version>
```

Artifacts are generated in `dist/<version>/artifacts/` with a shared
`SHA256SUMS` file.

## Naming Rules

Binary archives:

- `loramapr-receiver_<version>_<os>_<arch>.tar.gz`
- `loramapr-receiver_<version>_windows_amd64.zip`

Linux systemd layout archives:

- `loramapr-receiver_<version>_linux_<arch>_systemd.tar.gz`

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

## Suggested URL Pattern

Suggested host path convention:

- `https://downloads.loramapr.com/receiver/<channel>/<version>/<artifact-file>`

This is compatible with existing cloud catalog patterns and keeps receiver
versioning explicit in URL path.
