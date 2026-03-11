# Release Artifacts and Cloud Manifest Mapping

This document maps receiver release outputs to the cloud-side receiver artifact
manifest consumed by onboarding flows.

## Artifact Generation

Use:

```bash
packaging/release/build-artifacts.sh <version> [channel]
```

Enable Pi appliance image output:

```bash
PI_GEN_DIR=/path/to/pi-gen ENABLE_PI_IMAGE=1 \
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

Linux Debian packages:

- `loramapr-receiver_<version>_linux_amd64.deb`
- `loramapr-receiver_<version>_linux_arm64.deb`
- `loramapr-receiver_<version>_linux_armv7.deb` (`armhf` package architecture)

Raspberry Pi appliance image artifacts (when Pi image build is enabled):

- `loramapr-receiver_<version>_pi_arm64.img.xz`
- `loramapr-receiver_<version>_pi_arm64.image-metadata.json`

Manifest and metadata outputs:

- `cloud-manifest.fragment.json`
- `release-metadata.json`

`release-metadata.json` includes:

- `receiverVersion`
- `channel`
- `gitCommit` (if available)
- `buildDate` (if provided)
- `buildID` (if provided)
- artifact counts by platform grouping

Validation helper:

- `packaging/debian/validate-deb.sh <deb-file>`
- `packaging/pi/image/validate-image.sh <image-artifact>`

## Cloud Manifest Fields

When publishing to `loramapr-cloud` receiver artifact catalog, map each artifact:

- `receiverVersion`: `<version>`
- `channel`: `stable` or `beta`
- `platform`: one of `raspberry_pi`, `linux`, `macos`, `windows`
- `arch`: `amd64`, `arm64`, or `armv7`
- `kind`: `binary`, `systemd_layout`, `deb_package`, or `appliance_image`
- `downloadUrl`: hosted URL to artifact file
- `checksumSha256`: sha256 from `SHA256SUMS`
- `signatureUrl`: optional (future signing pipeline)
- `recommended`: policy-controlled flag

Cloud-ready mapping is produced directly by `cloud-manifest.fragment.json`. This
fragment already includes platform/arch/checksum/relative URL for each artifact,
including Raspberry Pi aliases for Linux arm64/armv7 systemd and `.deb` outputs.

## Suggested URL Pattern

Suggested host path convention:

- `https://downloads.loramapr.com/receiver/<channel>/<version>/<artifact-file>`

This is compatible with existing cloud catalog patterns and keeps receiver
versioning explicit in URL path.

The generated `relativeUrl` values in the manifest fragment are based on this
path pattern (`receiver/<channel>/<version>/<artifact-file>`).

For signed Linux/Pi publication flow and channel index metadata generation, see:

- `packaging/distribution/README.md`
- `docs/linux-pi-distribution.md`
