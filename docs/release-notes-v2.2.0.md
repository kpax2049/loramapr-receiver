# LoRaMapr Receiver v2.2.0 (Raspberry Pi Appliance GA)

Date: 2026-03-11

## Highlights

- Added automated Raspberry Pi image artifact path integrated with release flow:
  - `ENABLE_PI_IMAGE=1` support in `packaging/release/build-artifacts.sh`
  - versioned Pi image artifact output:
    - `loramapr-receiver_<version>_pi_arm64.img.xz`
    - `loramapr-receiver_<version>_pi_arm64.image-metadata.json`
- Added Pi image artifact validation helper:
  - `packaging/pi/image/validate-image.sh`
- Hardened appliance first-boot behavior for headless LAN setup:
  - appliance hostname default `loramapr-receiver`
  - Avahi/mDNS HTTP service advertisement on port `8080`
  - portal/discovery guidance aligned to `.local` and LAN IP fallback
- Extended diagnostics taxonomy for appliance-first boot states:
  - `pairing_not_completed`
  - `network_unavailable`
  - `portal_unavailable`
- Added local network probe integration across runtime, CLI, and support snapshot.
- Extended release manifest and distribution flow for Pi appliance image artifacts:
  - manifest `kind=appliance_image`
  - publish summary Pi image URL support
  - publish verify support with `PI_IMAGE_REQUIRED=1`

## Supported v2.2.0 Appliance Path

- Flash Pi image (`arm64`) to SD card
- Boot on LAN
- Open setup portal at:
  - `http://loramapr-receiver.local:8080`
  - fallback `http://<pi-lan-ip>:8080`
- Enter pairing code and progress to steady state

## Alternate Supported Path (Unchanged)

- Debian-family existing-OS package install via APT (`v2.1.0` path)

## Not in Scope

- Full CI-hosted `pi-gen` image production on every push
- macOS/Windows installer changes
