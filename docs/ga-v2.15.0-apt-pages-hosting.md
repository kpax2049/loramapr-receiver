# v2.15.0 Plan: Pages-Backed APT Host Under downloads.loramapr.com

## Context

Current install docs and bootstrap flow already target:

- `https://downloads.loramapr.com/apt/stable`

But when that hostname is not yet serving the signed APT tree, fresh Pi OS Lite
installs fail during bootstrap (`key fetch` and `apt update`).

Existing repo capabilities already in place:

- native `.deb` builds for `amd64`, `arm64`, `armv7/armhf`
- signed APT tree generation (`pool/`, `dists/`, `Release`, `InRelease`)
- publish/verify scripts and bootstrap helper

Missing link:

- operational static hosting at `downloads.loramapr.com` with Pages-compatible
  deployment path and maintainer docs.

## Target Strategy

Temporary hosting model for APT/static release outputs:

1. Keep public package URL stable:
   - `https://downloads.loramapr.com/apt/stable`
2. Generate signed static distribution tree in-repo.
3. Deploy static tree to GitHub Pages.
4. Bind custom domain `downloads.loramapr.com` to Pages.
5. Keep URL path model stable so future migration to VPS/object-store/CDN does
   not require client URL changes.

## Expected Published Layout

Published static root:

- `apt/<channel>/pool/main/l/loramapr-receiver/*.deb`
- `apt/<channel>/dists/<suite>/main/binary-*/Packages{,.gz}`
- `apt/<channel>/dists/<suite>/Release`
- `apt/<channel>/dists/<suite>/InRelease` and `Release.gpg` (signed path)
- `apt/<channel>/loramapr-archive-keyring.{asc,gpg}`
- `receiver/<channel>/<version>/...` (manifest/checksums/release metadata)
- `CNAME` (`downloads.loramapr.com`)
- `.nojekyll`

## Publication/Deployment Model

1. Build release artifacts:
   - `packaging/release/build-artifacts.sh <version> <channel>`
2. Publish signed distribution tree:
   - `packaging/distribution/publish.sh <version> <channel>`
3. Validate signed tree:
   - `packaging/distribution/verify.sh <version> <channel>`
4. Validate Pages-ready output:
   - `packaging/distribution/pages/verify-pages-tree.sh <version> <channel>`
5. Deploy Pages output via GitHub Actions Pages workflow.

## Migration Safety

Client-facing URLs stay unchanged:

- bootstrap script and docs continue using `downloads.loramapr.com`.
- only DNS/backend hosting target changes later.
- receiver install/update commands and cloud links remain stable.

## Fallback/Advanced

- Manual `.deb` install remains available as fallback.
- Manual tarball/systemd path remains advanced fallback.
- No receiver runtime architecture change in this milestone.

## Files/Areas to Change

Scripts:

- `packaging/distribution/publish.sh`
- `packaging/distribution/verify.sh` (if needed for Pages checks)
- `packaging/distribution/pages/verify-pages-tree.sh` (new)
- `packaging/linux/scripts/bootstrap-apt.sh`

Workflows:

- `.github/workflows/apt-pages-deploy.yml` (new)
- `.github/workflows/distribution-validate.yml` (Pages-tree validation)

Docs:

- `docs/linux-pi-distribution.md`
- `docs/publish-guide.md`
- `packaging/distribution/README.md`
- `packaging/distribution/apt/README.md`
- `docs/README.md`
- release notes + smoke-test docs for v2.15.0

## Done Target

Fresh Pi OS Lite installs can run bootstrap successfully because
`downloads.loramapr.com/apt/stable` resolves to a real signed APT tree served
through GitHub Pages.
