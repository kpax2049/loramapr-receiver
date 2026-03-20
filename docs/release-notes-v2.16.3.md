# Release Notes v2.16.3

Date: 2026-03-20

This patch hardens receiver runtime behavior for Meshtastic USB reconnect/hangup
conditions observed on Raspberry Pi.

## Highlights

- Meshtastic serial open path now uses `O_NOCTTY` on Linux/macOS to avoid
  daemon termination from TTY hangup semantics during USB node reboot/reset.
- Runtime now explicitly ignores `SIGHUP` so transient serial hangups do not
  terminate `loramapr-receiverd`.
- Packaged systemd unit now includes:
  - `RestartForceExitStatus=SIGHUP`
  - keeps existing restart policy and startup/shutdown timeout hardening.
- Packaging validation now enforces the new systemd hardening directive.

## Operational Impact

- Restarting or reconnecting the home Meshtastic USB device should no longer
  drop the receiver portal/service due to `SIGHUP`.
- Pairing and forwarding behavior outside serial reconnect scenarios is
  unchanged.
