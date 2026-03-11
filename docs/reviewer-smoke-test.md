# Reviewer Smoke Test Guide (v2.1.0)

This guide validates Linux/Pi Existing-OS GA behavior from artifact build to
pairing-ready runtime.

## Prerequisites

- Build host with Go and Debian packaging tools (`dpkg-deb`, `dpkg-scanpackages`)
- Debian-family test host (Debian/Ubuntu/Raspberry Pi OS) with systemd
- Optional reachable `loramapr-cloud` environment for real pairing/forwarding

## 1. Build and Validate Artifacts

```bash
GOCACHE=/tmp/go-build-cache GOTMPDIR=/tmp GO_BIN=/usr/local/go/bin/go \
  packaging/release/build-artifacts.sh v2.1.0-smoke stable
packaging/debian/validate-deb.sh dist/v2.1.0-smoke/artifacts/loramapr-receiver_v2.1.0-smoke_linux_amd64.deb
packaging/debian/validate-lifecycle.sh dist/v2.1.0-smoke/artifacts/loramapr-receiver_v2.1.0-smoke_linux_amd64.deb
```

Expected under `dist/v2.1.0-smoke/artifacts/`:

- linux `.deb` packages (`amd64`, `arm64`, `armv7`)
- linux systemd layout tarballs (fallback path)
- `SHA256SUMS`
- `cloud-manifest.fragment.json`
- `release-metadata.json`

## 2. Publish and Verify Repository Output

```bash
SIGNING_MODE=none packaging/distribution/publish.sh v2.1.0-smoke stable
SIGNING_REQUIRED=0 packaging/distribution/verify.sh v2.1.0-smoke stable
```

Expected output trees:

- `dist/published/receiver/stable/v2.1.0-smoke/`
- `dist/published/apt/stable/pool/main/l/loramapr-receiver/`
- `dist/published/apt/stable/dists/stable/main/binary-*/`

## 3. Install from APT Repository (Primary Path)

For local unsigned smoke repo:

```bash
REPO_PATH="$(pwd)/dist/published/apt/stable"
echo "deb [trusted=yes] file://${REPO_PATH} stable main" \
  | sudo tee /etc/apt/sources.list.d/loramapr-receiver-smoke.list
sudo apt-get update
sudo apt-get install -y loramapr-receiver
```

For real signed hosted repo, use keyring flow from
`packaging/distribution/apt/README.md`.

## 4. Verify Service and Pairing-Ready State

```bash
sudo systemctl status loramapr-receiverd --no-pager
curl -sS http://127.0.0.1:8080/healthz
curl -sS http://127.0.0.1:8080/readyz
curl -sS http://127.0.0.1:8080/api/status | jq
```

Expected:

- service active
- portal reachable
- receiver reports pairing-ready state when unpaired

## 5. Local Portal Pairing Submission

```bash
curl -sS -X POST http://127.0.0.1:8080/api/pairing/code \
  -H 'Content-Type: application/json' \
  -d '{"pairingCode":"LMR-TEST-CODE"}'
curl -sS http://127.0.0.1:8080/api/status | jq
```

Expected: pairing phase and diagnostics fields update consistently.

## 6. Cloud and Node Checks (If Environment Available)

1. Confirm `/api/status` reaches paired steady state after valid cloud flow.
2. Confirm Meshtastic component transitions toward `connected` when device is attached.
3. Confirm packet forwarding fields update (`last_packet_queued`, `last_packet_ack`).

## 7. Diagnostics Capture

```bash
loramapr-receiverd doctor -config /etc/loramapr/receiver.json
loramapr-receiverd doctor -config /etc/loramapr/receiver.json -json | jq
loramapr-receiverd support-snapshot -config /etc/loramapr/receiver.json -out /tmp/receiver-support.json
cat /tmp/receiver-support.json | jq
```

Expected:

- failure code/summary/hint are human-readable
- support snapshot is redacted (no secret values)

## 8. Lifecycle Operations

```bash
sudo apt-get remove -y loramapr-receiver
sudo test -f /etc/loramapr/receiver.json
sudo apt-get install -y loramapr-receiver
sudo systemctl status loramapr-receiverd --no-pager
sudo apt-get purge -y loramapr-receiver
```

Expected:

- `remove` keeps config/state
- reinstall starts service again
- `purge` clears config/state per lifecycle policy

## 9. Fallback Path Sanity (Advanced)

Validate that fallback/manual artifacts are still present:

```bash
ls dist/v2.1.0-smoke/artifacts/loramapr-receiver_v2.1.0-smoke_linux_amd64_systemd.tar.gz
```

Fallback tarball path remains advanced/manual, not primary.
