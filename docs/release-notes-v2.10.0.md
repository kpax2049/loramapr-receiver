# LoRaMapr Receiver v2.10.0 (Embedded Home Auto Session Milestone 2)

Release date: 2026-03-12

## Highlights

- Hardened Home Auto Session startup reconciliation with explicit outcomes:
  - `clean_idle`
  - `active_recovered_unverified`
  - pending start/stop recovery states
  - inconsistent-state degraded handling
- Added persisted pending-action recovery model for restart-safe control retries:
  - pending action kind/trigger/reason/dedupe/since
  - last successful action metadata
- Added geofence flap protection and GPS validity handling:
  - `missing`, `invalid`, `stale`, `boundary_uncertain`, `valid`
  - boundary uncertainty suppression near geofence edge
- Hardened cloud/API error behavior:
  - retryable failures enter bounded cooldown with preserved pending action
  - non-retryable failures enter degraded mode with explicit blocked reason
  - stop path handles already-resolved cloud stop responses safely
- Expanded status/portal/doctor/support surfaces with M2 context:
  - reconciliation state
  - pending action visibility
  - blocked reason
  - GPS status and reason

## Scope and Safety

- Packet forwarding remains primary and non-blocking.
- Home Auto Session remains optional and embedded.
- Milestone 2 does not expand policy scope beyond one geofence, explicit tracked
  node IDs, and one active auto session per receiver.
