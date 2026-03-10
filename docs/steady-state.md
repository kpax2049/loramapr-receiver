# Steady-State Ingest, Heartbeat, and Status Reporting

This document describes receiver steady-state cloud communication behavior.

## Cloud Paths

Once pairing reaches `steady_state`, the runtime uses persisted cloud values:

- ingest path: `cloud.ingest_endpoint` (fallback `/api/meshtastic/event`)
- heartbeat path: `cloud.heartbeat_endpoint` (fallback `/api/receiver/heartbeat`)
- auth: `cloud.ingest_api_key_secret` via `x-api-key`

Ingest and heartbeat traffic are handled separately.

## Ingest Delivery

Meshtastic packet events are normalized and enqueued for ingest posting.

Queue behavior:

- in-memory bounded queue (default max 512 events)
- oldest events are dropped if queue is full
- delivery retries use exponential backoff for retryable failures
- non-retryable failures are dropped and surfaced as coarse runtime errors

Idempotency:

- each event uses deterministic idempotency key derived from packet identity
- key is sent as `x-idempotency-key`

## Heartbeat Reporting

Heartbeat is sent on runtime tick interval (`service.heartbeat`) and includes:

- runtime version/platform/arch
- local node ID + observed node IDs (from Meshtastic adapter)
- coarse status payload:
  - pairing phase
  - service mode
  - meshtastic state
  - ingest queue depth
  - coarse failure reason

## Runtime Status Fields

`/api/status` now includes cloud/forwarding observability fields:

- `cloud_reachable`
- `heartbeat_fresh`
- `last_heartbeat_sent`
- `last_heartbeat_ack`
- `ingest_queue_depth`
- `last_packet_queued`
- `last_packet_sent`
- `last_packet_ack`

These are used by the local portal progress screen.

## Guarantees and Limits

Provided in this version:

- temporary offline tolerance with retry/backoff
- bounded queue to prevent unbounded memory growth
- no secret leakage in runtime status API

Not yet provided:

- durable on-disk packet queue across process restarts
- exactly-once delivery semantics
