# Packaging

This directory tracks installation/runtime packaging assets for `loramapr-receiverd`
with Linux/systemd as the first-class path.

## Layout

- `linux/systemd/`: systemd unit assets
- `linux/scripts/`: install/uninstall helper scripts
- `docker/`: container build scaffolding
- `release/`: multi-platform artifact generation and checksums
- `pi/`: Raspberry Pi appliance image scaffolding
- `macos/launchd/`: launchd placeholders
- `windows/`: Windows service placeholders

## Linux-first Service Model

Normal install path is expected to place files at:

- binary: `/usr/bin/loramapr-receiverd`
- config: `/etc/loramapr/receiver.json`
- state: `/var/lib/loramapr/receiver-state.json`
- logs: `/var/log/loramapr` (or journald)
- systemd unit: `/etc/systemd/system/loramapr-receiverd.service`

`loramapr-receiverd install` and `loramapr-receiverd uninstall` generate/remove
the service/config assets and can target an alternate root via `--target-root`
for packaging workflows.
