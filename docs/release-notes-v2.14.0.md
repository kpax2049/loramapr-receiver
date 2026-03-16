# LoRaMapr Receiver v2.14.0 (Raspberry Pi OS Lite Strategy Shift)

## Summary

`v2.14.0` shifts Raspberry Pi guidance to one first-class install path:

- official Raspberry Pi OS Lite + LoRaMapr Receiver package install

The custom Receiver Image path is now deprecated/paused and removed from active
build/release/test/public recommendation surfaces.

## What Changed

- Added Pi strategy/deprecation plan:
  - `docs/ga-v2.14.0-pi-os-lite-strategy.md`
- Simplified Raspberry Pi install guidance around one canonical existing-OS path
  with package install and service startup.
- Added small APT bootstrap helper for Pi OS Lite installs:
  - `packaging/linux/scripts/bootstrap-apt.sh`
- Removed Pi image as first-class output from active release flow.
- Updated build/distribution docs to center on Linux/Pi existing-OS artifacts.
- Updated reviewer smoke-test guidance to validate Pi OS Lite path.

## Operational Impact

- Recommended Raspberry Pi flow is now:
  1. Flash official Raspberry Pi OS Lite
  2. Run receiver package bootstrap/install path
  3. Open local portal and pair
- Existing package/upgrade/remove lifecycle behavior remains unchanged.
- Existing installs that still report `pi-appliance` remain readable in
  diagnostics/status; image path is simply no longer a promoted release channel.
