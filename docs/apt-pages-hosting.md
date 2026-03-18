# APT Hosting via GitHub Pages (Temporary)

LoRaMapr Receiver currently publishes the signed Debian-family APT repository as
static files and serves them through GitHub Pages behind:

- `https://downloads.loramapr.com/apt/stable`

This keeps client URLs stable while infrastructure is still evolving.

## 1. GitHub Prerequisites

Repository settings:

1. **Pages Source**: `GitHub Actions`
2. **Custom domain**: `downloads.loramapr.com`
3. **Enforce HTTPS**: enabled after certificate provisioning completes

Repository secrets for publish workflow:

- `APT_GPG_PRIVATE_KEY`: ASCII-armored private key used to sign APT metadata
- `APT_GPG_KEY_ID`: optional key id/fingerprint (workflow can auto-detect)

## 2. DNS Expectations

For subdomain `downloads.loramapr.com`, configure:

- `CNAME` record:
  - host: `downloads`
  - target: `<github-owner>.github.io`

Example for this repository owner:

- `downloads.loramapr.com CNAME kpax2049.github.io`

## 3. Publish Flow

Run workflow:

- `.github/workflows/apt-pages-deploy.yml`

Required inputs:

- `version` (for example `v2.15.0`)
- `channel` (`stable` or `beta`)
- `apt_suite` (optional; defaults to channel)
- `custom_domain` (default `downloads.loramapr.com`)

Workflow behavior:

1. Builds release artifacts.
2. Publishes signed distribution tree.
3. Emits Pages-ready tree (`dist/published-pages`).
4. Verifies distribution and Pages tree.
5. Deploys Pages artifact.

## 4. Verification

After deploy:

```bash
curl -fsSL https://downloads.loramapr.com/apt/stable/loramapr-archive-keyring.asc | head
curl -fsSL https://downloads.loramapr.com/apt/stable/dists/stable/InRelease | head
```

On fresh Raspberry Pi OS Lite:

```bash
curl -fsSL https://raw.githubusercontent.com/kpax2049/loramapr-receiver/main/packaging/linux/scripts/bootstrap-apt.sh | sudo bash
sudo apt-get update
sudo apt-get install -y loramapr-receiver
```

## 5. Future Host Migration

When moving away from Pages to VPS/object storage/CDN:

- keep public hostname and paths unchanged:
  - `https://downloads.loramapr.com/apt/<channel>`
- move only DNS/backend origin.
- no bootstrap/client URL rewrite should be required.
