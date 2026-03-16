# Raspberry Pi Image Scaffolding (Deprecated)

This directory contains historical Raspberry Pi appliance/image packaging
scaffolding for LoRaMapr Receiver.

## Status

Receiver image path is currently deprecated/paused:

- not part of active public build/release/publish/test flow
- not recommended in product-facing install docs

Current supported Raspberry Pi strategy is:

- official Raspberry Pi OS Lite
- install `loramapr-receiver` package on existing OS

Guide:

- `docs/linux-pi-distribution.md`

## Contents (Internal Reference)

- `receiver.appliance.json`: appliance-tuned runtime config
- `image/`: `pi-gen` stage and image-build helpers

GA planning history:

- `docs/ga-v2.2.0-raspberry-pi-appliance.md`
