# LoRaMapr Receiver v2.6.0 (Operational Automation and Notifications)

Release date: 2026-03-11

## Highlights

- Added receiver-side v2.6 automation/attention plan and implementation for
  cloud-aligned operational attention signaling.
- Added centralized receiver attention model:
  - states: `none`, `info`, `action_required`, `urgent`
  - categories: pairing/connectivity/authorization/lifecycle/node/forwarding/version/compatibility/service
- Enriched runtime status propagation and cloud heartbeat payload fields with
  support-safe attention and operational summary signals.
- Expanded support snapshot export with explicit attention fields to keep local
  and cloud triage terminology aligned.
- Improved local portal operator visibility:
  - Welcome/Progress/Troubleshooting now surface attention state and next-step guidance
  - `/api/ops` now returns operational checks plus attention payload
- Added coverage for attention derivation and portal attention rendering.
- Added v2.6 docs for automation role boundaries, status mapping, and reviewer
  smoke-test expectations.

## Scope and Safety

- No autonomous remediation agent was introduced.
- No remote execution behavior was added.
- No secrets were added to portal/CLI/support-bundle outputs.
- Existing install paths, lifecycle behavior, and update-status behavior remain
  intact.
