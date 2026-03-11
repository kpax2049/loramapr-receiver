# Raspberry Pi Appliance Path

This document defines the Raspberry Pi appliance/image path for LoRaMapr Receiver.

## Recommended User Flow

1. Flash the LoRaMapr Receiver Raspberry Pi image to an SD card.
2. Boot the Pi on the same LAN where setup will be performed.
3. Attach the Meshtastic device (USB serial) to the Pi.
4. From another device on the LAN, open the local portal at:
   - `http://loramapr-receiver.local:8080` (mDNS available)
   - or `http://<pi-lan-ip>:8080` (router/DHCP fallback)
5. Enter the pairing code from LoRaMapr Cloud onboarding.
6. Wait for pairing to move to `activated` then `steady_state`.
7. Confirm node detection and packet forwarding on the portal status page.

## Appliance Defaults

The appliance image uses the same `loramapr-receiverd` runtime and portal, with
configuration defaults tuned for headless LAN operation:

- `runtime.profile = appliance-pi`
- `service.mode = auto` (unpaired boot starts in setup path)
- `portal.bind_address = 0.0.0.0:8080`
- state path: `/var/lib/loramapr/receiver-state.json`
- systemd service enabled at boot

Reference config: `packaging/pi/receiver.appliance.json`

## First-Boot Behavior

- `loramapr-receiverd` starts from systemd during boot.
- If no durable receiver credentials exist, runtime remains in setup lifecycle and
  serves the local portal for pairing.
- Once paired, runtime transitions to steady-state forwarding and heartbeat loops
  without switching binaries or runtime forks.

## Image Build Scaffolding

Image build automation is provided in `packaging/pi/image/`.

Inputs:

- Linux arm64 systemd layout artifact:
  `loramapr-receiver_<version>_linux_arm64_systemd.tar.gz`
- Raspberry Pi OS image builder (`pi-gen`) workspace

Entry points:

- direct image build:
  - `packaging/pi/image/build-image.sh <version> [channel]`
- release-integrated image build:
  - `PI_GEN_DIR=/path/to/pi-gen ENABLE_PI_IMAGE=1 packaging/release/build-artifacts.sh <version> <channel>`

The build flow prepares `stage-loramapr` for `pi-gen`, runs image generation,
and emits:

- `loramapr-receiver_<version>_pi_arm64.img.xz`
- `loramapr-receiver_<version>_pi_arm64.image-metadata.json`

Image contents include:

- receiver binary + config + service unit
- appliance config defaults
- boot-time systemd enablement

## Cloud Onboarding Host-Choice Mapping

Cloud onboarding host-choice UI should expose a first-class
"Raspberry Pi Appliance" option that maps to receiver artifacts with:

- `platform = raspberry_pi`
- `arch = arm64` (primary)
- `channel = stable|beta`

The host-choice option should direct users to the Pi image artifact path rather
than advanced Linux package/manual service instructions.

## Scope and Limits (v1)

- Linux arm64 is the primary appliance image target.
- Wi-Fi provisioning UX is delegated to Raspberry Pi OS tooling/image setup.
- Image signing and secure-boot are future hardening work.
