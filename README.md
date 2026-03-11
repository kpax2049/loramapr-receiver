# LoRaMapr Receiver

Standalone runtime for deploying a **LoRaMapr Receiver** appliance that pairs with
`loramapr-cloud` and forwards decoded packets to the existing cloud ingest path.

Current scope is runtime skeleton and lifecycle wiring for long-running service
operation and first-run setup, with explicit extension points for pairing, cloud
steady-state loops, and Meshtastic integration.

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

Service/install command modes:

```bash
./bin/loramapr-receiverd run -config ./receiver.json
./bin/loramapr-receiverd doctor -config ./receiver.json
./bin/loramapr-receiverd status -config ./receiver.json
./bin/loramapr-receiverd install --dry-run --target-root /tmp/receiver-stage
```

Mode can be forced:

```bash
./bin/loramapr-receiverd -config ./receiver.json -mode setup
./bin/loramapr-receiverd -config ./receiver.json -mode service
```

Default `auto` mode chooses:

- `setup` when pairing state is not `steady_state`
- `service` when pairing state is `steady_state`

## Runtime Endpoints

- `GET /healthz` liveness
- `GET /readyz` readiness (mode/pairing aware)
- `GET /api/status` structured runtime status snapshot
- `POST /api/pairing/code` submit pairing code (`{"pairingCode":"LMR-..."}`)

Portal bind address is configured via `portal.bind_address`.

## Status

This repository currently contains:

- runtime skeleton with structured logging and clean startup/shutdown
- validated config with defaults and mode control
- atomic persistent local state (install ID, pairing phase, cloud endpoint, runtime profile)
- runtime status model for subsystem updates/reads
- health/readiness/status endpoints via embedded local HTTP server
- unit tests for config/state/status/runtime mode resolution

## Documentation

- [Architecture ADR](./docs/adr/0001-standalone-receiver-runtime.md)
- [Config and State Layout](./docs/runtime-config-state.md)
- [Pairing and Bootstrap Lifecycle](./docs/pairing-lifecycle.md)
- [Embedded Local Setup Portal](./docs/local-portal.md)
- [Meshtastic Adapter Behavior](./docs/meshtastic-adapter.md)
- [Steady-State Cloud Loop](./docs/steady-state.md)
- [Service and Install Model](./docs/service-install.md)
- [Release Artifact Mapping](./docs/release-artifacts.md)
- [Linux/Pi Distribution Path](./docs/linux-pi-distribution.md)
- [Raspberry Pi Appliance Path](./docs/raspberry-pi-appliance.md)
- [Release Notes](./docs/release-notes.md)
- [Reviewer Smoke Test Guide](./docs/reviewer-smoke-test.md)
