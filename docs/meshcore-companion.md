# MeshCore Companion setup and operation

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

This release's MeshCore dashboard is a connection-status page, not a BLE device
picker. Receiver provides local discovery, pairing, and forget operations under
its `/api/meshcore/ble/` API for a configured BlueZ adapter; the chosen address
must still be put in `meshcore.ble.peer_address`.

Before pairing, select **Release device** in the MeshCore portal tab. This
temporarily stops Receiver ownership without deleting an existing Bluetooth
bond. Pairing then uses the Receiver's local API and can accept the Companion's
six-digit PIN when required. Do not use `bluetoothctl` as the normal LoRaMapr
workflow.

After pairing, select **Resume receiver connection** and verify the connection
state again. The repaired pairing flow handles established bonds and serializes
pairing requests, but destructive physical revalidation is still pending; treat
pairing success as a local Bluetooth result and confirm a completed Companion
handshake in the portal.

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

Solicited MeshCore telemetry creates operational observations in LoRaMapr
Cloud. It can show an Operational Trail, but it is not automatically a trusted
track point or coverage sample. The request route can be Direct (zero-hop),
Flood, or Explicit path; the response route is unknown. Route hashes are useful
evidence, not device or repeater identities.

Receiver-local RSSI/SNR can be correlated to tagged telemetry using ordered and
temporal evidence. This association is heuristic rather than deterministic.
Cloud currently uses only validated high-confidence persisted RF samples;
other local confidence levels remain under physical validation.

For Cloud map, trail, and trust details, see the Cloud repository's
`docs/meshcore-session-tracking.md`.
