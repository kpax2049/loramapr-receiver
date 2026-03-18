# Receiver Publish Guide

This guide is for maintainers publishing LoRaMapr Receiver artifacts.

For user install flow, use:

- [Linux/Pi Existing-OS Install Path](./linux-pi-distribution.md)

Receiver appliance image path is currently deprecated/paused and is not part of
active public release flow.

## 1. Build Release Artifacts

```bash
packaging/release/build-artifacts.sh <version> <channel>
```

Outputs in `dist/<version>/artifacts/` include release artifacts, checksums,
manifest fragment, and release metadata.

Optional GitHub release asset publication:

```bash
packaging/release/publish-github-release-assets.sh <version>
```

By default this publish path excludes deprecated Pi appliance image files.

## 2. Publish Signed Linux/Pi Distribution

```bash
GPG_KEY_ID=<maintainer-key-id> SIGNING_MODE=required \
  packaging/distribution/publish.sh <version> <channel>
```

Staged publication tree:

- `dist/published/receiver/<channel>/<version>/...`
- `dist/published/receiver/<channel>/channel-index.json`
- `dist/published/apt/<channel>/pool/...`
- `dist/published/apt/<channel>/dists/<suite>/...`
- `dist/published-pages/apt/<channel>/...` (Pages-ready mirror)
- `dist/published-pages/receiver/<channel>/<version>/...`
- `dist/published-pages/CNAME` + `.nojekyll`

## 3. Verify Publication

```bash
packaging/distribution/verify.sh <version> <channel>
packaging/distribution/pages/verify-pages-tree.sh <version> <channel>
```

## 4. Deploy to GitHub Pages (Temporary Host)

Use workflow:

- `.github/workflows/apt-pages-deploy.yml`

This deploys the signed static tree to GitHub Pages under custom domain
`downloads.loramapr.com`.

## 5. Cloud Artifact Mapping

Cloud should consume:

- `cloud-manifest.fragment.json`
- URL pattern `receiver/<channel>/<version>/<artifact-file>`

Preferred kind for onboarding/install:

- `deb_package` for existing-OS package path

## 6. Diagnostics Sanity Check

Before final release announcement:

```bash
loramapr-receiverd doctor -config /etc/loramapr/receiver.json
loramapr-receiverd support-snapshot -config /etc/loramapr/receiver.json -out /tmp/receiver-support.json
```

## Related References

- [Release Artifact Mapping](./release-artifacts.md)
- [Support and Troubleshooting Workflow](./support-operations-workflow.md)
- [Version, Channel, and Upgrade Safety](./version-channel-upgrades.md)
- `packaging/distribution/apt/README.md`
- `packaging/distribution/pages/README.md`
- [APT Hosting via GitHub Pages](./apt-pages-hosting.md)
