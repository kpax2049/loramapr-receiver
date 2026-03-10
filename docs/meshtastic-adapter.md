# Meshtastic Adapter (Receiver-side)

This document describes the first Meshtastic adapter implementation in
`internal/meshtastic`.

## Scope

The adapter is intentionally isolated from cloud posting logic and provides:

- device detection
- connection lifecycle state
- node/status extraction
- packet/status event normalization into internal events

## Supported Transport Modes

Configured via `meshtastic.transport`:

- `serial` (default)
  - auto-detects likely serial devices on Linux/macOS
  - reads newline-delimited JSON events from the selected device path
- `json_stream`
  - reads newline-delimited JSON events from `meshtastic.device`
  - useful for sidecar-proxy or test pipes/files
- `disabled`
  - adapter stays inactive

## Detection Strategy

If `meshtastic.device` is explicitly configured and exists, it is used directly.

Otherwise, for `serial` mode:

- Linux patterns:
  - `/dev/serial/by-id/*`
  - `/dev/ttyACM*`
  - `/dev/ttyUSB*`
- macOS patterns:
  - `/dev/cu.usbmodem*`
  - `/dev/cu.usbserial*`
  - `/dev/tty.usbmodem*`
  - `/dev/tty.usbserial*`

## Connection Lifecycle

Adapter state values:

1. `not_present`
2. `detected`
3. `connecting`
4. `connected`
5. `degraded`

State is exposed through runtime status so the portal can show node/device
progress during onboarding.

## Normalized Internal Event Model

Two event kinds:

- `packet`
  - normalized fields include source node, destination node, port, payload bytes,
    receive timestamp, and optional metadata (RSSI/SNR/hop info)
- `status`
  - local node ID and observed node IDs

Expected incoming event shape is newline-delimited JSON records containing
`type: "packet"` or `type: "status"`.

## Known Limits (v1 skeleton)

- No direct protobuf/native Meshtastic protocol integration yet.
- Serial mode currently expects an upstream process/bridge to emit JSON lines.
- Adapter attempts reconnects with bounded delays and reports coarse errors.

This keeps the runtime boundary stable while allowing a later drop-in native
transport implementation without changing cloud/runtime orchestration APIs.
