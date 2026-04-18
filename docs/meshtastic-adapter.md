# Meshtastic Adapter (Receiver-side)

This document describes the first Meshtastic adapter implementation in
`internal/meshtastic`.

## Scope

The adapter is intentionally isolated from cloud posting logic and provides:

- device detection
- connection lifecycle state
- node/status extraction
- packet/status event normalization into internal events
- read-only home-node config summary extraction for field-node onboarding

## Supported Transport Modes

Configured via `meshtastic.transport`:

- `serial` (default)
  - direct native Meshtastic serial protocol path (advanced/compat mode)
  - passive/read-only by default (`meshtastic.bootstrap_write=false`)
  - optional throttled startup bootstrap request when
    `meshtastic.bootstrap_write=true`
  - Linux serial open no longer asserts DTR/RTS by default; set
    `LORAMAPR_MESHTASTIC_ASSERT_DTR_RTS=true` only for hardware that requires it
- `bridge` (packaged Linux/Pi default)
  - auto-detects likely serial devices on Linux/macOS
  - runs a supervised bridge subprocess that emits NDJSON
  - bridge startup performs a best-effort native `want_config` bootstrap write
    to prompt status/packet stream on nodes that stay quiet until requested
  - receiver consumes bridge output through the same normalization pipeline used
    for `json_stream`
  - default bridge command is internal:
    `loramapr-receiverd meshtastic-bridge -device <detected-device>`
  - optional override via `meshtastic.bridge_command` and
    `meshtastic.bridge_args`
- `json_stream`
  - reads newline-delimited JSON events from `meshtastic.device`
  - compatibility/test mode for sidecar-proxy pipes or fixture files
- `disabled`
  - adapter stays inactive

## Detection Strategy

If `meshtastic.device` is explicitly configured and exists, it is used directly.

Otherwise, for `serial` and `bridge` modes:

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
  - optional home-node config summary when upstream status/config events include:
    - region
    - primary channel summary
    - PSK presence state
    - LoRa share settings (if present)
    - Meshtastic share URL text (if present)

`serial` mode normalizes native `FromRadio` protobuf frames directly.

`bridge` mode uses the same normalized model, but frames are decoded in a
separate bridge subprocess and sent as NDJSON.

`json_stream` mode accepts newline-delimited JSON records containing either:

- normalized `type: "packet"` / `type: "status"` records
- Meshtastic packet-like compat records (for example `fromId` + `decoded`)
  emitted by older bridge pipelines

## Known Limits

- Native serial handling is intentionally narrow and supports the receiver’s
  primary onboarding/forwarding flow. It does not implement full Meshtastic
  device management.
- Home-node channel/config summary in native mode depends on what the connected
  node reports over the serial API stream.
- Adapter attempts reconnects with bounded delays and reports coarse errors.
- Share URL generation is not synthesized by receiver. Receiver only exposes
  share values that were actually reported by connected node status/config data.
- When config/share data is unavailable, receiver falls back to manual summary
  guidance and explicit unavailable reason.

## Security Notes

- Meshtastic share URLs can embed channel secrets.
- Receiver treats raw share URL/QR text as local-only operator data.
- Cloud heartbeat and support snapshot outputs include redacted/safe hints only.

This keeps the runtime boundary stable while allowing a later drop-in native
transport implementation without changing cloud/runtime orchestration APIs.
