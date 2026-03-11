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

This normal path is explicitly SSH-optional; setup should complete from the
portal without shell access.

## Appliance Defaults

The appliance image uses the same `loramapr-receiverd` runtime and portal, with
configuration defaults tuned for headless LAN operation:

- `runtime.profile = appliance-pi`
- `service.mode = auto` (unpaired boot starts in setup path)
- `portal.bind_address = 0.0.0.0:8080`
- hostname: `loramapr-receiver`
- state path: `/var/lib/loramapr/receiver-state.json`
- systemd service enabled at boot
- Avahi/mDNS enabled for `.local` discovery

Reference config: `packaging/pi/receiver.appliance.json`

## First-Boot Behavior

- `loramapr-receiverd` starts from systemd during boot.
- If no durable receiver credentials exist, runtime remains in setup lifecycle and
  serves the local portal for pairing.
- Once paired, runtime transitions to steady-state forwarding and heartbeat loops
  without switching binaries or runtime forks.

Expected first-boot diagnostics surfaces:

- portal Troubleshooting page
- `GET /api/status`
- `loramapr-receiverd doctor` (advanced/fallback)

Appliance-specific failure visibility includes:

- `network_unavailable`
- `pairing_not_completed`
- `portal_unavailable`
- `node_detected_not_connected`
- `no_serial_device_detected`
- `receiver_credential_revoked`
- `receiver_disabled`
- `receiver_replaced`

Lifecycle recovery on appliance (no SSH normal path):

- use portal Troubleshooting action "Reset And Re-pair"
- re-enter a fresh pairing code in portal

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

## Artifact Verification and Publication

Pi appliance releases publish the image artifact alongside standard release
checksums:

- `loramapr-receiver_<version>_pi_arm64.img.xz`
- `SHA256SUMS`
- optional detached signatures (`*.asc`) when signing is enabled

Verify downloaded image before flashing:

```bash
sha256sum -c SHA256SUMS --ignore-missing
```

Maintainer publish verification (including Pi image validation):

```bash
PI_IMAGE_REQUIRED=1 packaging/distribution/verify.sh <version> <channel>
```

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
- Wi-Fi provisioning is expected through Raspberry Pi Imager first-boot settings
  (SSID/password/country), not an SSH-first setup flow.
- Image signing and secure-boot are future hardening work.

## Difference vs Existing-OS Path

- Appliance image path:
  - flash image, boot Pi, open local portal, pair
  - optimized for novice setup with minimal host administration
- Existing-OS package path (`docs/linux-pi-distribution.md`):
  - apt install on a pre-existing Debian-family OS
  - preferred when user manages their own OS lifecycle and package updates
