# LoRaMapr Receiver v2.16.8 (Passive Serial Safety Hotfix)

Date: 2026-03-20

This patch prioritizes startup safety for Raspberry Pi + direct USB Meshtastic
nodes.

## Highlights

- Added `meshtastic.bootstrap_write` config flag.
  - default: `false` (passive serial mode)
  - when `false`, receiver does not send startup bootstrap writes to the node
- Serial startup behavior is now passive-first for package defaults.
- Existing optional bootstrap behavior remains available behind explicit opt-in
  (`meshtastic.bootstrap_write=true`), with throttling still applied.

## Operator Impact

- Reduces risk of node instability during receiver startup/restart on USB.
- Keeps portal/pairing/cloud workflow independent from aggressive serial writes.

## Notes

- This patch is intentionally conservative and Linux/Pi focused.
- Meshtastic ingest remains dependent on node-provided native frame traffic.
