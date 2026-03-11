# Raspberry Pi Packaging Scaffolding

This directory contains Raspberry Pi appliance/image packaging scaffolding for
LoRaMapr Receiver.

## Purpose

Make Raspberry Pi the novice-friendly recommended Meshtastic host path while
reusing the same `loramapr-receiverd` runtime and local setup portal.

## Contents

- `receiver.appliance.json`: appliance-tuned runtime config
- `image/`: image-build helper and `pi-gen` stage scaffolding

## Inputs

Expected release artifact input for image build:

- `loramapr-receiver_<version>_linux_arm64_systemd.tar.gz`

This artifact is produced by the release pipeline and used to construct the Pi
appliance image.

Release image output:

- `loramapr-receiver_<version>_pi_arm64.img.xz`

GA planning reference:

- `docs/ga-v2.2.0-raspberry-pi-appliance.md`
