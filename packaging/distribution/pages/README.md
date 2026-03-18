# GitHub Pages Publication (Temporary APT Host)

This directory contains validation helpers for publishing LoRaMapr Receiver's
signed static distribution tree to GitHub Pages under
`https://downloads.loramapr.com`.

## Pages Tree Source

`packaging/distribution/publish.sh` emits:

- canonical published tree: `dist/published/`
- Pages-ready mirror: `dist/published-pages/` (default)

Pages-ready tree root includes:

- `apt/<channel>/...` (signed APT repository)
- `receiver/<channel>/<version>/...` (release metadata/artifacts)
- `CNAME` (`downloads.loramapr.com` by default)
- `.nojekyll`

## Validate Pages Tree

```bash
packaging/distribution/pages/verify-pages-tree.sh <version> <channel>
```

To require APT signatures during validation:

```bash
SIGNING_REQUIRED=1 packaging/distribution/pages/verify-pages-tree.sh <version> <channel>
```

## Deploy

Use the GitHub Actions workflow:

- `.github/workflows/apt-pages-deploy.yml`

It builds artifacts, signs/publishes the distribution tree, validates it, then
deploys `dist/published-pages/` to GitHub Pages.
