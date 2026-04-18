# LoRaMapr Receiver v3.3.1 Release Notes

Date: 2026-04-18

## Scope

Patch hardening for Linux USB-serial interoperability with Meshtastic boards
that can reset when DTR/RTS lines are asserted.

## Changes

- Linux serial open path no longer asserts DTR/RTS control lines by default.
- Added explicit override env var for advanced cases:
  - `LORAMAPR_MESHTASTIC_ASSERT_DTR_RTS=true`
- Added focused unit tests for serial control-line env parsing behavior.
- Updated Meshtastic/runtime config docs with new Linux serial behavior.

## Why this matters

Some USB-serial boards (including certain RAK workflows) can rapidly
disconnect/re-enumerate when line-control behavior is too aggressive. This
patch reduces reset risk while keeping an explicit escape hatch for operators
who need legacy behavior.
