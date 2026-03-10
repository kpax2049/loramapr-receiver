# ADR 0001: Standalone LoRaMapr Receiver Runtime

- Status: Accepted (bootstrap)
- Date: 2026-03-10
- Product: LoRaMapr Receiver
- Binary: `loramapr-receiverd`

## Context

This repository is the standalone product runtime for LoRaMapr Receiver. The cloud
ingest architecture is already established in `loramapr-cloud` and must remain
unchanged: receiver nodes post packets/status to backend APIs.

This runtime must be deployable as a product service, not a developer-only tool.
The normal operator path should not require cloning the repository, hand-writing
environment files, or manually authoring service definitions.

## Decision

Build a small single-binary service (`loramapr-receiverd`) with an embedded local
setup portal and explicit internal boundaries:

- `internal/runtime`: process lifecycle and orchestration
- `internal/config`: persisted runtime configuration and defaults
- `internal/state`: local state and pairing/status persistence
- `internal/meshtastic`: Meshtastic transport abstraction
- `internal/cloudclient`: cloud API delivery for packets and heartbeat/status
- `internal/webportal`: local setup/status HTTP portal

This ADR defines responsibilities and phased packaging without fully implementing
installer mechanics yet.

## Runtime Responsibilities

`loramapr-receiverd` is responsible for:

1. starting as an OS service foreground process
2. loading persistent config and state from local disk
3. exposing local setup/status portal
4. connecting to a Meshtastic source (serial/network transport)
5. converting adapter events into backend ingest payloads
6. posting packets to cloud ingest endpoints
7. posting periodic heartbeat/status events
8. preserving local health and pairing state across restarts

Out of scope for this bootstrap phase:

- full UX implementation for setup portal pages
- full Meshtastic protocol implementation
- end-user installer scripts/packages
- cloud API redesign

## Pairing and Bootstrap Lifecycle

Target lifecycle:

1. **First boot / unpaired**
   - Service starts with local default config.
   - Portal is reachable on local address.
   - Cloud send operations are limited until paired credentials exist.
2. **Pairing requested**
   - Operator accesses portal.
   - Receiver obtains one-time pairing token/device claim data.
   - Runtime exchanges token with cloud to receive durable receiver identity and API credentials.
3. **Paired**
   - Credentials persisted locally (secure file permissions).
   - Runtime transitions to normal packet forward + heartbeat behavior.
4. **Recovery / re-pair**
   - If credentials revoked/invalid, runtime enters degraded mode.
   - Portal exposes actionable status and re-pair flow.

## Local Setup Portal Role

Portal is an embedded HTTP server in the same binary and serves product setup needs:

- pairing workflow entrypoint for first-time bootstrap
- local status page/API for support and diagnostics
- basic network/device/cloud health indicators
- constrained local administrative actions (e.g., trigger re-pair)

Portal is not the source of truth for long-term analytics; cloud remains canonical.

## Meshtastic Adapter Role

`internal/meshtastic` provides a stable runtime boundary between product logic and
transport details:

- handles device/network connection details
- emits normalized packet events (`source`, `payload`, `received_at`)
- reports adapter health state for portal and heartbeat
- supports pluggable transport modes (serial first, later others)

Runtime and cloud client must not depend on transport-specific protocol internals.

## Heartbeat and Status Behavior

Heartbeat must be periodic and lightweight:

- default interval: 30s (configurable)
- includes receiver identity, pairing state, runtime status, adapter health
- updates local last-success timestamp and last error state

Behavior expectations:

- packet-forward failures do not crash process by default
- heartbeat failures are retried on next tick
- severe startup misconfiguration fails fast

## Service and Install Modes

Runtime supports two execution modes:

1. **Interactive/dev mode**
   - launched manually (`go run` or local binary execution)
   - local config path and storage path defaults are acceptable
2. **Packaged service mode (target path)**
   - managed by OS service manager (systemd first)
   - packaged config/state locations and service unit provided by release artifacts
   - operator should only install package and follow setup portal

## Packaging and Release Phases

Phase plan:

1. **Phase 0 (this repo bootstrap)**
   - project layout, runtime boundaries, ADR/spec, minimal compile/run scaffold
2. **Phase 1**
   - real pairing API flow
   - actual Meshtastic adapter implementation
   - local portal pages/API for onboarding
3. **Phase 2**
   - system package + service installer baseline
   - default filesystem layout and secure credential storage behavior
4. **Phase 3**
   - signed update/release pipeline and platform matrix expansion

## Consequences

Positive:

- clear code landing zones for incremental prompts
- product runtime orientation from day one
- limited coupling between adapter/cloud/runtime concerns

Tradeoffs:

- additional interface boundaries upfront
- temporary placeholder behavior until real pairing and adapter logic lands
