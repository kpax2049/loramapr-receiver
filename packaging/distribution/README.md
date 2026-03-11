# Linux/Pi Distribution Publishing

This directory defines Linux/Pi-first publication for LoRaMapr Receiver release
outputs.

## Model

Distribution publish now emits both:

- static release artifacts for cloud/manual download:
  - `receiver/<channel>/<version>/...`
- signed APT repository outputs for Debian-family install/update:
  - `apt/<channel>/pool/...`
  - `apt/<channel>/dists/<suite>/...`

Static per-version key files:

- `SHA256SUMS`
- `cloud-manifest.fragment.json`
- `release-metadata.json`
- optional Pi image artifact:
  - `loramapr-receiver_<version>_pi_arm64.img.xz`
  - `loramapr-receiver_<version>_pi_arm64.image-metadata.json`
- optional detached signatures (`*.asc`)

APT metadata key files:

- `Packages` and `Packages.gz` per architecture
- `Release`
- `InRelease` and `Release.gpg` (when signed)
- `loramapr-archive-keyring.asc` and `.gpg` (when signed)

## Publish Steps

1. Build artifacts:
   - `packaging/release/build-artifacts.sh <version> [channel]`
2. Publish static and APT trees:
   - `GPG_KEY_ID=<key-id> SIGNING_MODE=required packaging/distribution/publish.sh <version> [channel]`
3. Verify publication output:
   - `packaging/distribution/verify.sh <version> [channel]`
   - if Pi image is expected in this release: `PI_IMAGE_REQUIRED=1 packaging/distribution/verify.sh <version> [channel]`
4. Sync `dist/published/` to artifact hosting (object storage/CDN).

## Signing Behavior

`publish.sh` and APT scripts support:

- `SIGNING_MODE=required`: fail without GPG + key
- `SIGNING_MODE=optional`: sign if available/key configured, otherwise continue
- `SIGNING_MODE=none`: do not sign

No signing keys are stored in repository files.

## Cloud Onboarding Mapping

Cloud onboarding should reference URLs from:

- `publish-summary.json`
- `cloud-manifest.fragment.json`

Recommended stable URL patterns:

- static artifacts: `https://downloads.loramapr.com/receiver/<channel>/<version>/<artifact-file>`
- apt repo root: `https://downloads.loramapr.com/apt/<channel>/`
- Pi image (if published): `https://downloads.loramapr.com/receiver/<channel>/<version>/loramapr-receiver_<version>_pi_arm64.img.xz`

## Install and APT Docs

- Linux/Pi install flow: `docs/linux-pi-distribution.md`
- APT script details: `packaging/distribution/apt/README.md`

## Future Work

- beta-channel promotion gates for APT publication
- macOS notarized distribution path
- Windows signed installer/service packaging path
