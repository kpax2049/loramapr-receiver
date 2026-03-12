# LoRaMapr Receiver

Standalone runtime for deploying a **LoRaMapr Receiver** appliance that pairs with
`loramapr-cloud` and forwards decoded packets to the existing cloud ingest path.

Current scope includes pairing/bootstrap lifecycle, embedded local portal,
Meshtastic adapter integration, steady-state ingest/heartbeat loops,
Linux/Pi GA packaging paths, lifecycle management, and update/upgrade safety
reporting, plus support-bundle export, coarse operational diagnostics, and
automation-aligned attention/remediation signaling.

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
./bin/loramapr-receiverd reset-pairing -config ./receiver.json
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
- `GET /api/ops` coarse operational checks
- `POST /api/pairing/code` submit pairing code (`{"pairingCode":"LMR-..."}`)
- `POST /api/lifecycle/reset` reset/deauthorize local pairing state

Portal bind address is configured via `portal.bind_address`.

## Status

This repository currently contains:

- service runtime with structured logging and clean startup/shutdown
- pairing/bootstrap/activation state machine with restart-safe persistence
- lifecycle handling for revoked/disabled/replaced receivers + reset/re-pair flow
- Meshtastic detection/connection and packet normalization boundary
- steady-state ingest, heartbeat, and receiver/node status reporting with retry/backoff
- embedded local setup/status portal and diagnostics taxonomy
- support-snapshot export with redaction and compatibility snapshot fallback
- coarse operational checks across doctor/status/portal
- Linux/Pi existing-OS `.deb`/APT path and Pi appliance image path scaffolding
- version/channel/build reporting, upgrade-safe schema handling, and thin update-status reasoning

## Documentation

- [Architecture ADR](./docs/adr/0001-standalone-receiver-runtime.md)
- [v2.1.0 GA Plan: Linux/Pi Existing-OS](./docs/ga-v2.1.0-linux-pi-existing-os.md)
- [v2.2.0 GA Plan: Raspberry Pi Appliance](./docs/ga-v2.2.0-raspberry-pi-appliance.md)
- [v2.3.0 Plan: Receiver Lifecycle Management](./docs/ga-v2.3.0-receiver-lifecycle-management.md)
- [v2.4.0 Plan: Update Channels and Upgrade Safety](./docs/ga-v2.4.0-update-channels-upgrade-safety.md)
- [v2.5.0 Plan: Support and Operations Maturity](./docs/ga-v2.5.0-support-operations-maturity.md)
- [v2.6.0 Plan: Operational Automation and Notifications](./docs/ga-v2.6.0-operational-automation-notifications.md)
- [Config and State Layout](./docs/runtime-config-state.md)
- [Pairing and Bootstrap Lifecycle](./docs/pairing-lifecycle.md)
- [Receiver Lifecycle Management](./docs/receiver-lifecycle.md)
- [Embedded Local Setup Portal](./docs/local-portal.md)
- [Meshtastic Adapter Behavior](./docs/meshtastic-adapter.md)
- [Steady-State Cloud Loop](./docs/steady-state.md)
- [Diagnostics and Failure Taxonomy](./docs/diagnostics.md)
- [Support and Operations Workflow](./docs/support-operations-workflow.md)
- [Version, Channel, and Upgrade Safety](./docs/version-channel-upgrades.md)
- [Service and Install Model](./docs/service-install.md)
- [Debian Package Lifecycle Behavior](./docs/linux-package-lifecycle.md)
- [Release Artifact Mapping](./docs/release-artifacts.md)
- [Linux/Pi Distribution Path](./docs/linux-pi-distribution.md)
- [Raspberry Pi Appliance Path](./docs/raspberry-pi-appliance.md)
- [Publish Guide](./docs/publish-guide.md)
- [Release Notes](./docs/release-notes.md)
- [Release Notes v2.6.0](./docs/release-notes-v2.6.0.md)
- [Release Notes v2.5.0](./docs/release-notes-v2.5.0.md)
- [Release Notes v2.4.0](./docs/release-notes-v2.4.0.md)
- [Release Notes v2.3.0](./docs/release-notes-v2.3.0.md)
- [Release Notes v2.2.0](./docs/release-notes-v2.2.0.md)
- [Release Notes v2.1.0](./docs/release-notes-v2.1.0.md)
- [Release Notes v1.1.0](./docs/release-notes-v1.1.0.md)
- [Reviewer Smoke Test Guide](./docs/reviewer-smoke-test.md)
