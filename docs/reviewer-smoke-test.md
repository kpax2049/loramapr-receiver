# Reviewer Smoke Test Guide

This guide is a short local validation path for receiver runtime integration.

## Prerequisites

- Go installed (tested with `/usr/local/go/bin/go`)
- Repository checked out
- Optional: sibling `../loramapr-cloud` if testing full pairing/ingest integration

## 1. Build and Test

```bash
GOCACHE=/tmp/go-build-cache GOTMPDIR=/tmp GO_BIN=/usr/local/go/bin/go make test
GOCACHE=/tmp/go-build-cache GOTMPDIR=/tmp GO_BIN=/usr/local/go/bin/go make build
```

Expected: all tests pass and `bin/loramapr-receiverd` is produced.

## 2. Start Receiver Locally

```bash
cp receiver.example.json receiver.local.json
./bin/loramapr-receiverd run -config ./receiver.local.json
```

Expected logs: runtime starts, portal bind shown, lifecycle enters running state.

## 3. Validate Local Endpoints

In another shell:

```bash
curl -sS http://127.0.0.1:8080/healthz
curl -sS http://127.0.0.1:8080/readyz
curl -sS http://127.0.0.1:8080/api/status | jq
```

Expected:

- `/healthz` returns HTTP 200
- `/readyz` reflects setup/service mode readiness
- `/api/status` shows pairing phase and component states

## 4. Pairing Submission Path (Local UX/API)

```bash
curl -sS -X POST http://127.0.0.1:8080/api/pairing/code \
  -H 'Content-Type: application/json' \
  -d '{"pairingCode":"LMR-TEST-CODE"}'
```

Expected: HTTP 202/200 style acceptance; runtime status moves into pairing flow state.

## 5. Release Artifact Pipeline Check

```bash
GOCACHE=/tmp/go-build-cache GOTMPDIR=/tmp VERSION=v0.0.0-smoke \
  packaging/release/build-artifacts.sh
```

Expected outputs under `dist/v0.0.0-smoke/artifacts/`:

- platform archives for release matrix
- Linux `*_systemd.tar.gz` layout archives
- `SHA256SUMS`

## 6. Pi Appliance Scaffolding Check

```bash
cat packaging/pi/receiver.appliance.json
packaging/pi/image/build-image.sh --help || true
```

Expected: appliance profile config exists (`runtime.profile=appliance-pi`) and image
builder script is present for pi-gen staging.

## Notes

- Full pairing activation and ingest delivery require reachable cloud endpoints and valid pairing codes.
- For sandboxed environments, set `GOCACHE`/`GOTMPDIR` to writable paths (for example `/tmp`).
