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
  - failure taxonomy fields (`failureCode`, `failureSummary`, `failureHint`)
  - attention fields (`attentionState`, `attentionCategory`, `attentionCode`, `attentionHint`)
  - operational summary (`operationalStatus`, `operationalSummary`)

Heartbeat scheduling is independent from packet ingest. Even with no Meshtastic
packet traffic, runtime still sends periodic heartbeat ticks while paired in
`steady_state` (unless pairing/lifecycle state blocks it).

## Liveness and Retry Signals

Runtime now emits explicit tick/recovery logs for support triage:

- `status tick processed`
- `heartbeat tick skipped`
- `heartbeat tick sent`
- `heartbeat tick failed`
- `ingest retry scheduled`

Home Auto Session also emits decision/recovery logs:

- `home auto session decision update`
- `home auto session retry scheduled`
- `home auto session action blocked by lifecycle conflict`
- `home auto session action blocked by non-retryable cloud error`

## Runtime Status Fields

`/api/status` now includes cloud/forwarding observability fields:

- `receiver_version`
- `release_channel`
- `build_commit`
- `cloud_reachable`
- `heartbeat_fresh`
- `last_heartbeat_sent`
- `last_heartbeat_ack`
- `ingest_queue_depth`
- `last_packet_queued`
- `last_packet_sent`
- `last_packet_ack`
- `failure_code`
- `failure_summary`
- `failure_hint`
- `attention_state`
- `attention_category`
- `attention_code`
- `attention_summary`
- `attention_hint`
- `operational_status`
- `operational_summary`

These are used by the local portal progress screen.

## Guarantees and Limits

Provided in this version:

- temporary offline tolerance with retry/backoff
- bounded queue to prevent unbounded memory growth
- no secret leakage in runtime status API

Not yet provided:

- durable on-disk packet queue across process restarts
- exactly-once delivery semantics
