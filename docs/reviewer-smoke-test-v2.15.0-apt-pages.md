# Reviewer Smoke Test Guide (v2.15.0 APT Pages Host)

## 1. Local Build/Test Baseline

```bash
GOCACHE=/tmp/go-build-cache GOTMPDIR=/tmp make test
GOCACHE=/tmp/go-build-cache GOTMPDIR=/tmp make build
```

## 2. Publish + Verify Signed Distribution Trees

Build and publish a release candidate:

```bash
packaging/release/build-artifacts.sh v0.0.0-apt-pages stable
GPG_KEY_ID=<key-id> SIGNING_MODE=required packaging/distribution/publish.sh v0.0.0-apt-pages stable
SIGNING_REQUIRED=1 packaging/distribution/verify.sh v0.0.0-apt-pages stable
SIGNING_REQUIRED=1 packaging/distribution/pages/verify-pages-tree.sh v0.0.0-apt-pages stable
```

Verify output roots:

- `dist/published/apt/stable/...`
- `dist/published-pages/apt/stable/...`
- `dist/published-pages/CNAME` contains `downloads.loramapr.com`

## 3. GitHub Pages Deploy Path

Run workflow:

- `.github/workflows/apt-pages-deploy.yml`

Check:

1. workflow imports signing key and signs APT metadata
2. Pages deploy succeeds
3. CNAME/custom domain remains `downloads.loramapr.com`

## 4. DNS/HTTPS Validation

Validate custom domain resolves and serves repository files:

```bash
curl -fsSL https://downloads.loramapr.com/apt/stable/loramapr-archive-keyring.asc | head
curl -fsSL https://downloads.loramapr.com/apt/stable/dists/stable/InRelease | head
```

## 5. Fresh Raspberry Pi OS Lite Install

On a clean Raspberry Pi OS Lite host:

```bash
curl -fsSL https://raw.githubusercontent.com/kpax2049/loramapr-receiver/main/packaging/linux/scripts/bootstrap-apt.sh | sudo bash
sudo apt-get update
sudo apt-get install -y loramapr-receiver
systemctl status loramapr-receiverd --no-pager
```

Then verify:

1. local portal reachable (`http://loramapr-receiver.local:8080` or LAN IP)
2. pairing page is reachable/pairing-ready
3. package source uses `downloads.loramapr.com/apt/stable`

## 6. Fallback Path Sanity

Confirm docs still include fallback options but keep them secondary:

- manual APT source setup
- manual `.deb` install
