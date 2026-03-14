# LoRaMapr Receiver v2.13.0 (Meshtastic Field-Node Pairing Data)

Date: 2026-03-14

## Summary

This release adds receiver-side read-only exposure of connected home-node
Meshtastic communication settings to support field-node onboarding.

## Highlights

- Added normalized home-node config summary extraction from Meshtastic
  status/config events.
- Added portal visibility for field-node pairing data on Progress:
  - region
  - primary channel summary
  - PSK state
  - LoRa summary fields when available
  - share URL path when reported by node
- Added safe fallback behavior when config/share data is unavailable.
- Added diagnostics/doctor/status support for Meshtastic config summary and
  share availability.
- Added redaction handling so raw share URL/QR text is excluded from support
  snapshots and cloud-facing status payloads.

## Scope Limits

- Receiver remains read-only for Meshtastic config in this milestone.
- Receiver does not synthesize share URLs when node does not provide them.
- Full Meshtastic config editing remains out of scope.
