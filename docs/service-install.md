# Service and Install Model

This document defines the receiver install/service model for `loramapr-receiverd`.

## Command Modes

`loramapr-receiverd` supports:

- `run`: run service runtime (default mode if no subcommand)
- `install`: materialize Linux service/config/state layout
- `uninstall`: remove Linux service/config assets
- `doctor`: local diagnostics for config/state runtime prerequisites
- `status`: print persisted state summary JSON
- `support-snapshot`: export redacted diagnostics/support JSON bundle

## Linux-first Path (systemd)

Primary supported install path for Debian-family systems is native `.deb`
package install via APT.

Generated files:

- `/etc/loramapr/receiver.json`
- `/lib/systemd/system/loramapr-receiverd.service`
- directories:
  - `/var/lib/loramapr`
  - `/var/log/loramapr`

Manual/systemd tarball fallback still uses the existing `install` command path
when package-based install is not available.

## Advanced / Packaging Path

For staged builds/chroots/package assembly, commands support:

- `--target-root /path/to/stage`
- `--dry-run` to inspect planned operations

This enables package automation without writing directly into host root.

## Cross-platform Placeholders

Repository includes placeholder paths for future non-Linux service integration:

- macOS launchd: `packaging/macos/launchd/`
- Windows service packaging: `packaging/windows/`

Linux remains the only implemented service install path in this version.

## Raspberry Pi Appliance Path

Raspberry Pi appliance images use the same Linux/systemd service model with an
image-first distribution path. See `docs/raspberry-pi-appliance.md` and
`packaging/pi/` for image scaffolding and appliance defaults.
