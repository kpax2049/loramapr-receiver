# Release Notes v2.15.0

Date: 2026-03-18

## Summary

`v2.15.0` makes the Debian-family install path operational now by adding a
GitHub-Pages-backed publication flow for the signed APT repository while
preserving the public package origin:

- `https://downloads.loramapr.com/apt/stable`

## What Changed

- Added APT Pages hosting strategy spec:
  - `docs/ga-v2.15.0-apt-pages-hosting.md`
- Extended distribution publish flow to emit a Pages-ready static tree:
  - `dist/published-pages/` with `apt/`, `receiver/`, `CNAME`, `.nojekyll`
- Added Pages-tree verification helper:
  - `packaging/distribution/pages/verify-pages-tree.sh`
- Added GitHub Actions Pages deployment workflow:
  - `.github/workflows/apt-pages-deploy.yml`
- Added maintainer docs for Pages setup, DNS, and HTTPS:
  - `docs/apt-pages-hosting.md`
- Updated install/publish docs to treat `downloads.loramapr.com` as the active
  package origin.
- Improved bootstrap script errors for unreachable key/repo host.
- Extended CI distribution validation to check Pages-ready output.

## Operational Impact

- Fresh Raspberry Pi OS Lite installs can use the canonical bootstrap command
  once `downloads.loramapr.com` DNS is pointed to GitHub Pages and publish
  workflow has run.
- Public APT URL remains stable for future hosting migration.

## Maintainer Notes

- Configure repository secrets for signing:
  - `APT_GPG_PRIVATE_KEY`
  - `APT_GPG_KEY_ID` (optional)
- Use workflow:
  - `APT Pages Deploy`
- Validate locally with:
  - `packaging/distribution/verify.sh <version> <channel>`
  - `packaging/distribution/pages/verify-pages-tree.sh <version> <channel>`
