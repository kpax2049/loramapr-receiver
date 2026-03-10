# Service and Install Model

This document defines the receiver install/service model for `loramapr-receiverd`.

## Command Modes

`loramapr-receiverd` supports:

- `run`: run service runtime (default mode if no subcommand)
- `install`: materialize Linux service/config/state layout
- `uninstall`: remove Linux service/config assets
- `doctor`: local diagnostics for config/state runtime prerequisites
- `status`: print persisted state summary JSON

## Linux-first Path (systemd)

Primary supported install path:

1. package places binary at `/usr/bin/loramapr-receiverd`
2. run `loramapr-receiverd install --force`
3. run `systemctl daemon-reload`
4. run `systemctl enable --now loramapr-receiverd`

Generated files:

- `/etc/loramapr/receiver.json`
- `/etc/systemd/system/loramapr-receiverd.service`
- directories:
  - `/var/lib/loramapr`
  - `/var/log/loramapr`

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
