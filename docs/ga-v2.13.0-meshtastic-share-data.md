# v2.13.0 Plan: Receiver-Side Meshtastic Channel/Share Data Exposure

Milestone: `v2.13.0`

## Goal

Expose onboarding-relevant communication settings from the connected home
Meshtastic node so field-node setup can follow real home-node settings.

Preferred path:

- share-based setup using Meshtastic channel URL/QR text when available

Fallback path:

- read-only manual summary (region/channel/LoRa + PSK presence state)

## Current Adapter Reality

Current receiver-side Meshtastic integration is newline-delimited JSON event
normalization (`packet` + `status`) with no direct protobuf config API calls.

That means channel/config data is only available when upstream status/config
messages include it.

## Minimum Readable Summary (Target)

Read and expose, when present in Meshtastic status/config events:

- region
- primary channel name (+ index if present)
- PSK state (`present|not_set|unknown`)
- coarse LoRa share parameters (`preset`, bandwidth/SF/CR when present)
- share URL/text availability

## Share Representation Rules

- If event payload includes channel/share URL text, receiver exposes it on local
  portal for copy/QR usage.
- Receiver also stores a redacted share hint for support-safe outputs.
- Receiver does not attempt to invent or reverse-engineer share URLs when source
  data is absent.

## Local vs Cloud Exposure

Local portal/status surfaces:

- full share text may be shown locally (operator-facing)
- full manual config summary shown

Cloud-facing heartbeat/status:

- only coarse support-safe hints:
  - config available
  - region/channel/PSK state
  - share available bool
  - redacted share hint

Support bundle/doctor/status export:

- includes summary + redacted share hint
- excludes raw share URL/QR text

## Privacy/Security Boundaries

- Share URLs can embed channel secrets.
- Raw share URL/QR text is treated as sensitive:
  - not sent in cloud heartbeat payloads
  - not included in support snapshot exports
- Docs must warn operators to use share values only on trusted local networks
  and trusted devices.

## Unavailable-Config Behavior

When no config summary is present:

- status marks config as unavailable with explicit reason
- portal explains fallback/manual path
- troubleshooting hints guide operator to Meshtastic app manual sharing

## Landing Zones

Implementation touches:

- `internal/meshtastic/normalize.go`
- `internal/meshtastic/adapter.go`
- `internal/runtime/service.go`
- `internal/status/model.go`
- `internal/webportal/server.go`
- `internal/webportal/templates/progress.tmpl`
- `internal/webportal/templates/advanced.tmpl`
- `internal/diagnostics/snapshot.go`
- `cmd/loramapr-receiverd/main.go`
- tests in corresponding packages
- docs updates (`meshtastic-adapter`, `local-portal`, `diagnostics`, release notes,
  reviewer guide)
