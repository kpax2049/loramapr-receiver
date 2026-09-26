# LoRaMapr Receiver v2.17.0 - MeshCore Companion support

Date: 2026-09-26

## Highlights

- Added MeshCore Companion transport over Bluetooth Low Energy (BLE), including
  Cloud-managed Scan, Connect/Pair, Reconnect, Forget, Release, and Resume
  operations. The Receiver remains the authority for local BlueZ work.
- Added Session-managed telemetry polling with tagged request correlation,
  request-route and receiver-local RF evidence, and capture-time normalized
  delivery for LoRaMapr Cloud.
- Added automatic BLE state reconciliation and recovery, bounded safe runtime
  diagnostics, and persistent Linux/systemd operation with restart-on-failure.
- Added a durable normalized-event outbox with retry/backoff and a hot-path
  optimization that avoids redundant full-queue reconciliation.

## Cloud compatibility

v2.17.0 is the Receiver companion release for LoRaMapr Cloud 2.2.0.

Cloud deployments accepting normalized Receiver events must set:

```env
RECEIVER_EVENTS_V1_ENABLED=true
```

If event intake is disabled, the Receiver keeps normalized events durable and
retryable and surfaces a bounded Cloud-configuration diagnostic. Heartbeats
can remain healthy in that condition.

## Upgrade behavior

Preserve the existing Receiver configuration, state, and outbox when upgrading.
The durable systemd service is intended to restart the Receiver after failures;
it does not require replacing established pairing, BLE peer, or queued-event
state. Monitor normalized delivery acknowledgements and outbox backlog after a
Cloud or Receiver upgrade.

## Verification

Release validation covers MeshCore BLE management and recovery, Session polling
and correlated telemetry, durable normalized delivery, Cloud diagnostics, and
the Receiver's steady-state outbox CPU behavior.

## Deferred, non-blocking work

Repeater identity resolution, richer topology graphs, cross-Receiver semantic
deduplication, Receiver handoff/pinning, raw event/log viewing, and richer
response-route reconstruction remain future enhancements. They are not required
for MeshCore support in LoRaMapr 2.2.0.
