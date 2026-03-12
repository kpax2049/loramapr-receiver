# Raspberry Pi Appliance Path

This is the recommended install path for most first-time LoRaMapr Receiver
users.

You flash a prebuilt image, boot the Pi, open the local portal, and pair.

For install on an existing Linux host instead, use:

- [Linux/Pi Existing-OS Install Path](./linux-pi-distribution.md)

## Who Should Choose This Path

Choose Raspberry Pi Appliance when you want:

- minimal Linux administration
- a dedicated always-on receiver host
- setup from a phone/laptop browser without SSH

## First-Time Setup (Normal Path)

1. Flash the LoRaMapr Receiver Raspberry Pi image to an SD card.
2. (Optional but recommended) Use Raspberry Pi Imager advanced settings to
   preconfigure Wi-Fi/country/hostname.
3. Boot the Pi on the same LAN used for setup.
4. Connect your Meshtastic device by USB.
5. Open the local portal from another device:
   - `http://loramapr-receiver.local:8080` (preferred)
   - `http://<pi-lan-ip>:8080` (fallback)
6. Enter pairing code from LoRaMapr Cloud onboarding.
7. Wait until portal shows paired/steady status and verify forwarding.

Expected boot behavior:

- receiver service starts automatically at boot
- unpaired installs stay in setup mode with portal available
- paired installs continue forwarding on restart without re-pair

Home Auto Session inclusion:

- Embedded in the appliance runtime image.
- Default is off (`enabled=false`, `mode=off`).
- Enable/configure from local portal at `/home-auto-session`.

## Appliance Defaults

Appliance image uses the same `loramapr-receiverd` runtime with headless defaults:

- `runtime.profile = appliance-pi`
- `service.mode = auto`
- `portal.bind_address = 0.0.0.0:8080`
- hostname default: `loramapr-receiver`
- state path: `/var/lib/loramapr/receiver-state.json`
- systemd service enabled at boot
- Avahi/mDNS enabled for `.local` discovery

Reference config:

- `packaging/pi/receiver.appliance.json`

## If You Cannot Find the Portal

1. Wait 2-3 minutes after first boot and refresh.
2. Check router DHCP table and open `http://<pi-lan-ip>:8080`.
3. Confirm Pi and setup device are on the same LAN.
4. If still blocked, use local diagnostics from shell/console:
   - `systemctl status loramapr-receiverd`
   - `loramapr-receiverd doctor -config /etc/loramapr/receiver.json`

## Lifecycle Recovery (No SSH Normal Path)

If portal shows revoked/disabled/replaced state:

1. Open **Troubleshooting**.
2. Click **Reset And Re-pair**.
3. Submit a fresh pairing code.

## Image Artifacts and Verification

Pi appliance release artifacts include:

- `loramapr-receiver_<version>_pi_arm64.img.xz`
- `loramapr-receiver_<version>_pi_arm64.image-metadata.json`
- `SHA256SUMS`
- optional detached signatures (`*.asc`) when signing is enabled

Verify before flashing:

```bash
sha256sum -c SHA256SUMS --ignore-missing
```

## Build/Publication References (Maintainers)

- direct image build:
  - `packaging/pi/image/build-image.sh <version> [channel]`
- release-integrated image build:
  - `PI_GEN_DIR=/path/to/pi-gen ENABLE_PI_IMAGE=1 packaging/release/build-artifacts.sh <version> <channel>`
- publication verification:
  - `PI_IMAGE_REQUIRED=1 packaging/distribution/verify.sh <version> <channel>`

## Cloud Onboarding Mapping

Cloud host-choice should expose a first-class Raspberry Pi option mapped to:

- `platform = raspberry_pi`
- `arch = arm64`
- `kind = appliance_image`
