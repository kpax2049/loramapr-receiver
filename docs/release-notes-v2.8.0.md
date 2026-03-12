# LoRaMapr Receiver v2.8.0 (Multi-Receiver / Household / Team Operations)

Release date: 2026-03-12

## Highlights

- Added receiver-side multi-receiver identity model and propagation across:
  - local runtime status
  - cloud heartbeat/status payloads
  - portal identity display
  - doctor/status/support-snapshot outputs
- Added local naming support with stable precedence:
  - `runtime.local_name` override
  - persisted local hint
  - derived host/install fallback
- Added cloud label reflection (when provided) for:
  - receiver label
  - site label
  - group label
- Improved portal guidance for coexistence and replacement scenarios:
  - additional receiver onboarding
  - replaced/revoked/disabled interpretation
  - node attached elsewhere clarification
- Added multi-receiver docs/spec and reviewer coverage for identity and
  coexistence validation.

## Scope and Safety

- No runtime architecture redesign.
- No new install path or pairing model.
- Cloud remains source of truth for ownership/group/site policy.
- Receiver-side outputs remain support-safe (no secret material exposure).
