# Raspberry Pi Image Build

This directory contains Raspberry Pi appliance image build automation based on
the existing `pi-gen` stage integration.

## Release-oriented Build

From repository root:

```bash
VERSION=v2.2.0-rc1
CHANNEL=stable
PI_GEN_DIR=/path/to/pi-gen \
ENABLE_PI_IMAGE=1 \
packaging/release/build-artifacts.sh "${VERSION}" "${CHANNEL}"
```

This produces image output under:

- `dist/<version>/artifacts/loramapr-receiver_<version>_pi_arm64.img.xz`
- `dist/<version>/artifacts/loramapr-receiver_<version>_pi_arm64.image-metadata.json`

## Direct Image Build

```bash
PI_GEN_DIR=/path/to/pi-gen \
packaging/pi/image/build-image.sh <version> [channel]
```

Optional prep-only mode (no image build):

```bash
PI_GEN_DIR=/path/to/pi-gen \
PI_IMAGE_PREP_ONLY=1 \
packaging/pi/image/build-image.sh <version>
```

## Expected Image Contents

- LoRaMapr Receiver runtime preinstalled (`arm64` service layout payload)
- appliance config defaults (`runtime.profile=appliance-pi`)
- systemd service enabled at boot
- local portal bind configured for LAN access
- hostname defaults to `loramapr-receiver`
- mDNS advertisement (`loramapr-receiver.local`) via Avahi

## Headless Setup Expectations

- Wi-Fi credentials should be configured via Raspberry Pi Imager customization
  before first boot.
- Normal setup path does not require SSH; pairing is done via local portal.
- Fallback discovery is router-assigned LAN IP if `.local` name is unavailable.

## Validation

Validate produced image artifact:

```bash
packaging/pi/image/validate-image.sh dist/<version>/artifacts/loramapr-receiver_<version>_pi_arm64.img.xz
```

Validation checks compressed image integrity and basic size sanity.
