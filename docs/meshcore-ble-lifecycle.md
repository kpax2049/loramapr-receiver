# MeshCore BLE lifecycle

For `meshcore.transport: ble`, the receiver owns the configured BlueZ peer's
connection lifecycle. Pairing remains a separate local operation, and must be
started after the receiver has released its BLE connection. Releasing a
connection never removes the BlueZ device or its bond.

## Release for phone use

To hand the configured MeshCore Companion back to a phone without stopping the
receiver daemon, call:

```sh
curl -X POST http://127.0.0.1:8080/api/meshcore/adapter/release
```

Release stops active MeshCore tracking, cancels the active BLE attempt/session,
stops GATT notifications, and asks BlueZ to disconnect the configured peer. It
then suppresses every receiver reconnect attempt and telemetry tracking poll.
The action is idempotent and does not need a Bluetooth address in its request.

When finished with the phone, restore ordinary configured auto-connect with:

```sh
curl -X POST http://127.0.0.1:8080/api/meshcore/adapter/resume
```

Resume is also idempotent. It clears the in-memory release gate and returns to
the usual discovery/connect/handshake flow. Session-managed collection waits
for Cloud to reaffirm an active Session; it does not restore a remembered
tracking target locally. Release is intentionally not persisted, so a receiver
restart follows its configured auto-connect behavior.

## Status and shutdown

`GET /api/status` reports the MeshCore adapter with its existing
`connection_state`, `configured_device`, and `device` fields, plus explicit
`connected_device`,
`reconnect_suppressed` and `released_by_user`. A released adapter uses
`connection_state: "released"`, has no `connected_device`, and has no
`last_error`; an intentional release is not an adapter fault.

Normal daemon shutdown also stops tracking, cancels reconnect work, closes the
Companion session, stops GATT notifications, and sends BlueZ `Device1.Disconnect`.
Those operations use bounded waits so an unavailable BlueZ service cannot hold
the process indefinitely. Pairing is preserved in all cases.
