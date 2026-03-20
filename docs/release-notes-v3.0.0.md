# Release Notes v3.0.0

Date: 2026-03-21

This release introduces the next Linux/Pi stability iteration focused on
Meshtastic ingest isolation and better local portal responsiveness.

## Highlights

- Meshtastic transport now supports `bridge` mode as a first-class runtime path.
  - Packaged Linux/Pi default moved to `meshtastic.transport="bridge"`.
  - Bridge process is supervised and auto-restarted by the receiver runtime.
  - Default bridge command is internal:
    `loramapr-receiverd meshtastic-bridge -device <detected-device>`.
  - Optional command override supported with:
    - `meshtastic.bridge_command`
    - `meshtastic.bridge_args`

- Added internal bridge subcommand:
  - `loramapr-receiverd meshtastic-bridge -device <path>`
  - Reads native serial frames and emits normalized NDJSON for runtime ingest.

- Local portal update behavior moved from fixed 5s full-page polling to SSE:
  - New endpoint: `GET /api/events/status`
  - `/`, `/progress`, `/troubleshooting`, and `/advanced` now refresh on actual
    status change events.
  - Low-rate fallback refresh remains for non-SSE clients.

## Validation

- Added/updated tests for:
  - bridge command resolution and token substitution
  - bridge event record mapping
  - bridge transport compatibility path in adapter
  - SSE status event endpoint behavior in web portal tests
  - config parsing/roundtrip for bridge transport fields

## Notes

- `serial` and `json_stream` transports remain available.
- `bridge` is now the recommended packaged Linux/Pi default transport.

