# Linux/Pi Distribution Publishing

This directory defines the Linux/Pi-first signed publication flow for LoRaMapr
Receiver artifacts.

## Model

Current distribution model is a signed static artifact repository rooted at:

- `receiver/<channel>/<version>/...`

Key files per release version:

- `SHA256SUMS`
- `cloud-manifest.fragment.json`
- `release-metadata.json`
- optional detached signatures (`*.asc`)

Channel-level metadata:

- `receiver/<channel>/channel-index.json`
- optional `channel-index.json.asc`

This is an "APT-equivalent signed metadata skeleton" for current tarball-based
runtime distribution and is intended to back cloud onboarding download targets.

## Publish Steps

1. Build artifacts:
   - `packaging/release/build-artifacts.sh <version> [channel]`
2. Publish static repository tree:
   - `GPG_KEY_ID=<key-id> SIGNING_MODE=required packaging/distribution/publish.sh <version> [channel]`
3. Verify publication output:
   - `packaging/distribution/verify.sh <version> [channel]`
4. Sync `dist/published/` to artifact hosting (for example object storage/CDN).

## Signing Behavior

`publish.sh` supports:

- `SIGNING_MODE=required`: fail without GPG + key
- `SIGNING_MODE=optional`: sign if available/key configured, otherwise continue
- `SIGNING_MODE=none`: do not sign

No signing keys are stored in repository files.

## Cloud Onboarding Mapping

Cloud onboarding should reference URLs from:

- `publish-summary.json`
- `cloud-manifest.fragment.json`

Recommended stable URL pattern:

- `https://downloads.loramapr.com/receiver/<channel>/<version>/<artifact-file>`

## Raspberry Pi and Linux Install Docs

See:

- `docs/linux-pi-distribution.md`

## Future Work

- native APT `.deb` repository metadata once `.deb` artifacts are first-class
- macOS notarized distribution path
- Windows signed installer/service packaging path
