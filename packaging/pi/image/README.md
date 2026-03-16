# Raspberry Pi Image Build (Deprecated)

This directory contains historical Raspberry Pi appliance image build
scaffolding based on `pi-gen`.

## Status

Receiver image path is currently deprecated/paused and not part of active
public release flow.

Current supported Raspberry Pi strategy is package install on official
Raspberry Pi OS Lite:

- `docs/linux-pi-distribution.md`

## Internal-Only Reference

If image build work is resumed later, key entry points are:

- `packaging/pi/image/build-image.sh`
- `packaging/pi/image/stage-loramapr/`
- `packaging/pi/image/validate-image.sh`

Any future reactivation should explicitly restore CI/release/docs guidance.
