# Reviewer Smoke Test Guide (v2.2.0)

This guide validates Raspberry Pi Appliance GA end-to-end behavior. The
existing-OS package path remains a supported alternate path and is listed in the
final section.

## Prerequisites

- Build host with Go and release dependencies
- `pi-gen` workspace for real image generation
- Raspberry Pi 4/5/400 (`arm64`) + SD card
- LAN with DHCP and internet access
- Optional Meshtastic device for node/packet checks

## 1. Build Artifacts (Including Pi Image)

```bash
VERSION=v2.2.0-smoke
CHANNEL=stable
PI_GEN_DIR=/path/to/pi-gen \
ENABLE_PI_IMAGE=1 \
packaging/release/build-artifacts.sh "${VERSION}" "${CHANNEL}"
```

Expected under `dist/${VERSION}/artifacts/`:

- `loramapr-receiver_${VERSION}_pi_arm64.img.xz`
- `loramapr-receiver_${VERSION}_pi_arm64.image-metadata.json`
- standard platform archives and checksums/manifest metadata

Validate the Pi image artifact:

```bash
packaging/pi/image/validate-image.sh dist/${VERSION}/artifacts/loramapr-receiver_${VERSION}_pi_arm64.img.xz
```

## 2. Publish and Verify Distribution Output

```bash
SIGNING_MODE=none packaging/distribution/publish.sh "${VERSION}" "${CHANNEL}"
PI_IMAGE_REQUIRED=1 SIGNING_REQUIRED=0 packaging/distribution/verify.sh "${VERSION}" "${CHANNEL}"
```

Expected:

- published static receiver tree
- published APT tree (existing-OS alternate path)
- Pi image verification passes from published path

## 3. Flash and Boot Pi Appliance

1. Flash `loramapr-receiver_${VERSION}_pi_arm64.img.xz` to SD card.
2. In Raspberry Pi Imager custom settings, preconfigure Wi-Fi/country/hostname
   settings if needed.
3. Boot Pi on LAN and wait for service startup.

## 4. Discover and Open Local Portal (No SSH Path)

From another LAN device:

- `http://loramapr-receiver.local:8080`
- fallback `http://<pi-lan-ip>:8080`

Expected:

- welcome/progress pages load
- status shows pairing-ready state when unpaired
- troubleshooting page shows actionable guidance if blocked

## 5. Pairing and Runtime Progress

1. Submit pairing code in portal (or `POST /api/pairing/code`).
2. Observe phase progression:
   - `unpaired` -> `pairing_code_entered` -> `bootstrap_exchanged` -> `activated` -> `steady_state`
3. Confirm no persistent failure code after successful pairing.

## 6. Node and Packet Checks

With Meshtastic device attached:

1. Confirm status transitions from detection toward `connected`.
2. Confirm packet telemetry fields advance:
   - `last_packet_queued`
   - `last_packet_ack`
3. Confirm no persistent `events_not_forwarding` state.

## 7. Diagnostics Capture

```bash
loramapr-receiverd doctor -config /etc/loramapr/receiver.json
loramapr-receiverd doctor -config /etc/loramapr/receiver.json -json | jq
loramapr-receiverd support-snapshot -config /etc/loramapr/receiver.json -out /tmp/receiver-support.json
cat /tmp/receiver-support.json | jq
```

Expected:

- appliance-specific failure states are explicit (`network_unavailable`,
  `pairing_not_completed`, `portal_unavailable`, etc.)
- support snapshot remains redacted (no secrets)

## 8. Existing-OS Alternate Path (Sanity)

Confirm existing-OS package path is still intact:

```bash
VERSION=v2.2.0-smoke
CHANNEL=stable
SIGNING_MODE=none packaging/distribution/publish.sh "${VERSION}" "${CHANNEL}"
SIGNING_REQUIRED=0 packaging/distribution/verify.sh "${VERSION}" "${CHANNEL}"
```

Then install via APT on Debian-family host and confirm service/portal startup as
documented in `docs/linux-pi-distribution.md`.
