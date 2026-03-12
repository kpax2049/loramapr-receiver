# LoRaMapr Receiver v2.11.0 (Embedded Home Auto Session Milestone 3)

Release date: 2026-03-12

## Highlights

- Finalized Home Auto Session production control semantics:
  - explicit control states (`ready`, `pending_start`, `pending_stop`,
    `conflict_blocked`, `lifecycle_blocked`, etc.)
  - explicit active-state source (`cloud_acknowledged`,
    `local_recovered_unverified`, `conflict_unresolved`)
- Added receiver-side conflict/lifecycle handling for Home Auto Session control:
  - already-active start conflict handling
  - stop state-mismatch conflict handling
  - lifecycle blocked handling for revoked/disabled/replaced receiver identity
- Expanded control action observability:
  - `last_action`, `last_action_result`, `last_action_at`
  - richer tracked node/control status in portal, `doctor`, `status`, and
    support snapshot
- Updated local portal Home Auto Session UX for production use:
  - control/source/reconciliation visibility
  - tracked node state and geofence summary
  - clearer observe-vs-control guidance and conflict/lifecycle explanations
- Confirmed install-path inclusion model:
  - feature is embedded in both supported Linux/Pi install paths
  - remains optional and default-off in package and appliance defaults

## Scope and Safety

- Packet forwarding remains primary and non-blocking.
- Home Auto Session remains local-config-first and opt-in.
- Milestone 3 does not expand policy model beyond one geofence, explicit tracked
  node IDs, and one active auto session per receiver.
