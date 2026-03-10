# LoRaMapr Receiver

Standalone runtime for deploying a **LoRaMapr Receiver** appliance that pairs with
`loramapr-cloud` and forwards decoded packets to the existing cloud ingest path.

Current scope is bootstrap architecture and project scaffolding, not full runtime
feature implementation.

## Product Direction

- Product binary: `loramapr-receiverd`
- Runtime target: small single-binary service
- Local UX target: embedded setup portal for pairing/configuration
- Cloud integration: receiver posts to backend ingest architecture (unchanged)

## Repository Layout

- `cmd/loramapr-receiverd`: service entrypoint
- `internal/config`: runtime and persisted configuration
- `internal/state`: local runtime/pairing state persistence
- `internal/cloudclient`: outbound API client to LoRaMapr cloud backend
- `internal/meshtastic`: Meshtastic transport/adapter boundary
- `internal/webportal`: embedded local setup portal
- `internal/runtime`: service orchestration loop
- `docs/`: architecture and ADRs
- `packaging/`: packaging and install mode artifacts (phased)

## Quick Start (Scaffold)

```bash
cp receiver.example.json receiver.json
go run ./cmd/loramapr-receiverd -config ./receiver.json
```

Or build:

```bash
go build -o bin/loramapr-receiverd ./cmd/loramapr-receiverd
./bin/loramapr-receiverd -config ./receiver.json
```

## Status

This repository currently contains:

- initial package layout
- runtime boundaries and interfaces
- architecture/spec ADR for upcoming implementation prompts
