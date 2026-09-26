# MeshCore Companion support for LoRaMapr 2.1.0

LoRaMapr Receiver can connect to a MeshCore Companion over Bluetooth Low
Energy (BLE). The intended baseline is stock/official MeshCore Companion
firmware on a BLE-capable Linux or Raspberry Pi host.

## Set up the Receiver connection

First pair the Receiver itself with LoRaMapr Cloud through the local portal's
**Pairing** tab. Then configure the MeshCore transport for the Companion you
want this Receiver to own:

```json
{
  "meshcore": {
    "transport": "ble",
    "ble": {
      "adapter": "hci0",
      "peer_address": "AA:BB:CC:DD:EE:FF"
    }
  }
}
```

`peer_address` is only the local Bluetooth locator. It is not MeshCore device
identity. The Receiver requires an explicit BLE peer; it does not choose one
automatically. The default adapter is `hci0` when no adapter is specified.

Open the local portal at `http://loramapr-receiver.local:8080` (or the host's
LAN address) and select **MeshCore**. Confirm that the configured device is
shown and that **MeshCore connection** reaches **Connected** with the Companion
session handshake ready.

## Discovering and pairing a Companion

LoRaMapr Cloud's Receiver panel is the normal device picker. A Scan request is
delivered on the authenticated Receiver heartbeat, and the Receiver performs
bounded BlueZ discovery locally. Choosing **Use device** persists the selected
Bluetooth address in the existing `meshcore.ble.peer_address` Receiver
configuration and wakes the normal Companion reconnect lifecycle. The Cloud
action uses an existing BlueZ bond when one is available. If the device needs
pairing, enter its six-digit PIN with the Use action; it is held only in the
pending Cloud control record and sent with the active Receiver command, is never
part of Receiver configuration or status, and is cleared from Cloud control
state after acknowledgement.

Receiver also retains local discovery, pairing, and forget operations under its
`/api/meshcore/ble/` API for recovery and setup work. A remote **Forget device**
removes the configured BlueZ peer and leaves `meshcore.transport: "ble"` enabled
without a selected `peer_address`, ready for a later Cloud-managed scan/use
action.

Before pairing, select **Release device** in the MeshCore portal tab. This
temporarily stops Receiver ownership without deleting an existing Bluetooth
bond. Pairing then uses the Receiver's local API and can accept the Companion's
six-digit PIN when required. Do not use `bluetoothctl` as the normal LoRaMapr
workflow.

After local pairing, use the Cloud panel to select the device and verify the
reported connection state. Pairing success is only a local Bluetooth result;
confirm a completed Companion handshake before treating the Receiver as ready.

## Sessions and telemetry

Start a MeshCore Session for the selected device in LoRaMapr Cloud. Cloud then
manages the Receiver's telemetry-collection intent. The MeshCore portal shows
receiver-local status, polling activity, and recent request-side route evidence.
Stopping the Session stops automatic collection.

You can release the Companion when another client needs it. Release preserves
the Bluetooth bond and prevents Receiver reconnects and telemetry polls. After
you resume, the Receiver returns to its normal connection flow; an active
Cloud Session must be reaffirmed before Session-managed collection starts.

## What the data means

MeshCore support is complete for LoRaMapr 2.1.0. Solicited telemetry creates
valid observed position evidence in LoRaMapr Cloud. Together with signed
adverts and other supported evidence, it is included in the complete Session
point stream used for Session maps, history, distance, analytics, playback, and
GeoJSON export. Evidence/trust/source metadata remains visible.

Verified DeviceCurrentPosition remains signed-only. The normal Device map and
history can use latest observed telemetry position without promoting it to a
verified current position or a coverage sample. Coverage eligibility remains a
separate strict Cloud policy.

The request route can be Direct (zero-hop), Flood, or Explicit path; the
response route is unknown. Route hashes are useful evidence, not device or
repeater identities.

Receiver-local RSSI/SNR can be correlated to tagged telemetry using ordered and
temporal evidence. This association is heuristic rather than deterministic.
Cloud currently uses only validated high-confidence persisted RF samples;
other local confidence levels are intentionally not projected.

For Cloud map, Session, and trust details, see the Cloud repository's
`docs/meshcore-session-tracking.md`.

## Delivery and recovery

Receiver persists normalized MeshCore observations to its durable outbox before
Cloud delivery. Cloud deployments require RECEIVER_EVENTS_V1_ENABLED=true to
accept that event contract. When disabled, the outbox remains durable/retryable
and the Receiver reports a bounded Cloud-configuration diagnostic rather than
a connectivity failure.

The outbox is bounded to 10,000 events / 64 MiB; monitor backlog health during
extended Cloud outages. The supported Linux/systemd service restarts on failure
and preserves configured state/outbox data. Mac sleep in local development is
not a production Receiver defect.

## Future work, not a 2.1.0 gap

Repeater identity resolution, richer route/topology reconstruction,
cross-Receiver semantic deduplication, Receiver handoff/pinning, raw
event/log viewing, solicited-telemetry trust promotion, and richer
response-route reconstruction remain future enhancements.
