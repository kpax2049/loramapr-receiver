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
- Pages-ready static mirror (default):
  - `dist/published-pages/apt/...`
  - `dist/published-pages/receiver/...`
  - `dist/published-pages/CNAME`
  - `dist/published-pages/.nojekyll`

Static per-version key files:

- `SHA256SUMS`
- `cloud-manifest.fragment.json`
- `release-metadata.json`
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
4. Verify Pages-ready publication output:
   - `packaging/distribution/pages/verify-pages-tree.sh <version> [channel]`
5. Deploy `dist/published-pages/` to GitHub Pages.

Note:

- deprecated Pi appliance image files are removed from normal publish output.

## Signing Behavior

`publish.sh` and APT scripts support:

- `SIGNING_MODE=required`: fail without GPG + key
- `SIGNING_MODE=optional`: sign if available/key configured, otherwise continue
- `SIGNING_MODE=none`: do not sign

Pages emission controls:

- `ENABLE_PAGES_LAYOUT=1` (default): emit `dist/published-pages`
- `PAGES_OUTPUT_ROOT=/path/to/pages-root`: override Pages output directory
- `PAGES_CNAME=downloads.loramapr.com`: custom-domain file content

No signing keys are stored in repository files.

## Cloud Onboarding Mapping

Cloud onboarding should reference URLs from:

- `publish-summary.json`
- `cloud-manifest.fragment.json`

Recommended stable URL patterns:

- static artifacts: `https://downloads.loramapr.com/receiver/<channel>/<version>/<artifact-file>`
- apt repo root: `https://downloads.loramapr.com/apt/<channel>/`

Temporary hosting model:

- GitHub Pages serves the static tree behind `downloads.loramapr.com`.
- Later migration to VPS/object-storage/CDN should keep the same hostname/paths.

## Install and APT Docs

- Linux/Pi install flow: `docs/linux-pi-distribution.md`
- APT script details: `packaging/distribution/apt/README.md`
- Pages deploy details: `packaging/distribution/pages/README.md`

## Future Work

- beta-channel promotion gates for APT publication
- macOS notarized distribution path
- Windows signed installer/service packaging path
