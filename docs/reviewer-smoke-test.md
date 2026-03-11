# Reviewer Smoke Test Guide

This guide validates the bridge-batch receiver flow from artifact build to
runtime diagnostics.

## Prerequisites

- Go installed
- Docker/systemd host optional for service-path checks
- Optional reachable `loramapr-cloud` environment for real pairing/forwarding

## 1. Build Artifact Creation

```bash
GOCACHE=/tmp/go-build-cache GOTMPDIR=/tmp GO_BIN=/usr/local/go/bin/go make test
GOCACHE=/tmp/go-build-cache GOTMPDIR=/tmp VERSION=v1.1.0-smoke CHANNEL=stable \
  packaging/release/build-artifacts.sh
```

Expected under `dist/v1.1.0-smoke/artifacts/`:

- linux amd64/arm64/armv7 archives
- linux systemd layout archives
- `SHA256SUMS`
- `cloud-manifest.fragment.json`
- `release-metadata.json`

## 2. Publish-Path Verification

```bash
SIGNING_MODE=none packaging/distribution/publish.sh v1.1.0-smoke stable
packaging/distribution/verify.sh v1.1.0-smoke stable
```

Expected output tree:

- `dist/published/receiver/stable/v1.1.0-smoke/`
- `dist/published/receiver/stable/channel-index.json`

## 3. Install/Service Startup (Linux path)

On Linux host or VM:

```bash
sudo tar -xzf dist/v1.1.0-smoke/artifacts/loramapr-receiver_v1.1.0-smoke_linux_amd64_systemd.tar.gz -C /
sudo systemctl daemon-reload
sudo systemctl enable --now loramapr-receiverd
sudo systemctl status loramapr-receiverd --no-pager
```

Expected: service active and portal bound per config.

## 4. Local Portal Pairing Path

```bash
curl -sS http://127.0.0.1:8080/healthz
curl -sS http://127.0.0.1:8080/readyz
curl -sS http://127.0.0.1:8080/api/status | jq
curl -sS -X POST http://127.0.0.1:8080/api/pairing/code \
  -H 'Content-Type: application/json' \
  -d '{"pairingCode":"LMR-TEST-CODE"}'
```

Expected: status shows pairing progression and diagnostics fields.

## 5. Cloud Connection and Node Detection

With real cloud + Meshtastic bridge stream configured:

1. Confirm `/api/status` transitions to paired steady state.
2. Confirm `components.meshtastic.state` moves to `connected`.
3. Confirm `cloud_reachable=true` and heartbeat fields update.

## 6. Packet Forwarding Check

Inject or observe Meshtastic packet events via configured transport.

Expected `/api/status` behavior:

- `ingest_queue_depth` rises briefly then returns toward zero
- `last_packet_queued` and `last_packet_ack` advance
- no persistent `events_not_forwarding` failure state

## 7. Diagnostics Capture

```bash
loramapr-receiverd doctor -config /etc/loramapr/receiver.json
loramapr-receiverd doctor -config /etc/loramapr/receiver.json -json | jq
loramapr-receiverd support-snapshot -config /etc/loramapr/receiver.json -out /tmp/receiver-support.json
cat /tmp/receiver-support.json | jq
```

Expected:

- diagnostics include failure code/summary/hint when setup is blocked
- support snapshot includes runtime/cloud/node summaries
- support snapshot omits secrets (API key, pairing code, activation token values)

## 8. Version/Channel Reporting

Check either `/api/status`, `status` command, or support snapshot for:

- `receiver_version`
- `release_channel`
- `build_commit`

These should match release artifact version/channel intent.
