# LoRaMapr Receiver Release Notes v2.12.0

Date: 2026-03-12

Milestone: Embedded Home Auto Session M4 (Minimal Cloud-Managed Control)

## Highlights

- Added receiver-side cloud-managed Home Auto Session config application path.
- Added deterministic precedence:
  - valid cloud-managed config first
  - local fallback when cloud config is missing/invalid
- Added persisted and support-safe config tracking:
  - effective source/version
  - cloud-config present marker
  - last fetched/applied versions
  - last config apply result/error
- Extended heartbeat/cloud status payload with Home Auto config-source/apply
  fields for cloud-side support alignment.
- Updated local portal Home Auto Session page with:
  - cloud-managed vs local-fallback visibility
  - config apply status/error hints
  - desired-vs-runtime blocked/degraded guidance
- Extended diagnostics surfaces (`doctor`, `status`, `support-snapshot`) with
  cloud-managed config state markers.

## Safety and Fallback Semantics

- Cloud-managed config is optional.
- If cloud config is unavailable, receiver keeps last effective config and
  reports fetch failure state.
- Invalid cloud config is rejected and local fallback remains active.
- Packet forwarding remains primary and non-blocking.

## State/Compatibility

- State schema updated to `7` with Home Auto config-source/version/apply markers.
- Existing state is migrated automatically to schema `7`.

## Reviewer Focus

Use [Reviewer Smoke Test Guide](./reviewer-smoke-test.md) and validate:

1. no cloud config -> `local_fallback`
2. valid cloud config -> `cloud_managed`
3. cloud config disables module -> disabled effective policy
4. invalid cloud config -> explicit fallback + apply error
5. cloud outage -> keep last effective config safely
