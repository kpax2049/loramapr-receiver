# v2.2.0 GA Plan: Raspberry Pi Appliance

Status: Draft for implementation
Date: 2026-03-11
Milestone: `v2.2.0`

## Goal

Promote the Raspberry Pi appliance path from scaffold/manual flow to a real,
published, verifiable install path for end users.

This milestone is Pi image-focused and keeps the Debian-family existing-OS
package path as a supported alternate install path.

## Current Appliance Path (Audited)

### What exists today

- Appliance runtime profile defaults are defined:
  - `runtime.profile = appliance-pi`
  - `portal.bind_address = 0.0.0.0:8080`
- `pi-gen` stage integration exists (`stage-loramapr`).
- Manual image prep flow exists via:
  - `packaging/pi/image/build-image.sh`
  - `packaging/pi/image/stage-loramapr/*`
- Receiver service and portal behavior are reused from the same runtime.
- Appliance and first-boot docs exist.

### What is missing for GA

1. No automated release-time Pi image artifact output.
2. No CI-produced Pi image artifact checks.
3. No integrated publish/sign/verify flow for Pi image artifacts.
4. No explicit cloud-manifest mapping for Pi image artifacts.
5. First-boot discovery/headless guidance needs stronger GA-level clarity.

## v2.2.0 Target Output

### Supported models/architecture

- Primary supported image architecture: Raspberry Pi OS 64-bit (`arm64`).
- Target hardware class for GA validation:
  - Raspberry Pi 4 Model B (4 GB+ recommended)
  - Raspberry Pi 5
  - Raspberry Pi 400
- Existing-OS package path remains the alternate path for broader model support
  (including 32-bit hosts).

### Image build flow

Release build should produce a versioned Pi appliance image artifact by
automating the existing `pi-gen` flow:

1. Build receiver release artifacts (including Linux `arm64` systemd layout).
2. Prepare `pi-gen` stage with receiver payload + appliance config.
3. Run `pi-gen` in deterministic mode.
4. Emit compressed image artifact (`.img.xz`) plus checksums/metadata.

### First-boot behavior

- Receiver service enabled at boot.
- Runtime starts in pairing-ready mode when unpaired.
- Portal available on LAN at `:8080`.
- Meshtastic detection starts automatically with serial transport defaults.

### Local discovery assumptions

- Hostname target: `loramapr-receiver.local` (mDNS via Avahi).
- Fallback discovery: router/DHCP-assigned IP address.
- Portal URL guidance:
  - `http://loramapr-receiver.local:8080`
  - `http://<lan-ip>:8080`

### Wi-Fi/headless setup expectations

- Normal path uses Raspberry Pi Imager first-boot customization for Wi-Fi, locale,
  and optional SSH (advanced only).
- No SSH required for normal setup; pairing happens via portal from another LAN
  device.

### Publication/signing/checksum expectations

- Publish versioned Pi image under existing channel/version structure.
- Emit `SHA256SUMS` entries for image artifacts.
- Emit detached signatures using existing signing conventions where enabled.
- Extend publish/verify scripts with Pi image-aware checks and summary metadata.

### How this differs from existing-OS install path

- Pi appliance path:
  - flash image, boot, open portal, pair
  - preinstalled runtime/service and appliance defaults
- Existing-OS path:
  - apt install on user-managed OS
  - package lifecycle and distro integration managed by host

## Implementation Landing Zones (Next Prompts)

### P-GA2: automated Pi image outputs

Add/modify:

- `packaging/pi/image/build-image.sh` (non-interactive release-mode outputs)
- `packaging/release/build-artifacts.sh` (invoke Pi image build when enabled)
- `packaging/pi/image/` (image output validation helper script)
- `docs/raspberry-pi-appliance.md` and `packaging/pi/image/README.md`

### P-GA3: first boot/discovery/headless hardening

Add/modify:

- `packaging/pi/receiver.appliance.json` (appliance defaults as needed)
- `packaging/pi/image/stage-loramapr/00-run.sh` (hostname/discovery-first setup)
- `internal/webportal/*` and diagnostics docs for appliance-specific guidance
- `docs/local-portal.md`, `docs/raspberry-pi-appliance.md`

### P-GA4: publish/sign/verify Pi image artifacts

Add/modify:

- `packaging/distribution/publish.sh` and `packaging/distribution/verify.sh`
- `packaging/distribution/README.md`
- Pi-specific publish helpers under `packaging/distribution/pi/` if needed
- `internal/release/manifest.go` and docs for Pi image artifact mapping

### P-GA5: final integration

Add/modify:

- consolidated docs for flash/first boot/discovery/verification
- `docs/release-notes-v2.2.0.md`
- reviewer smoke test guide updates for Pi appliance end-to-end path

## Concise Summary for Maintainers

- Current path: coherent scaffolding + manual `pi-gen` prep and appliance config.
- Gaps to GA: no automated image artifact build/publish/verify path.
- v2.2.0 target: real versioned, verifiable Pi image artifacts with first-boot
  pairing-ready behavior and clear LAN discovery guidance.
- Next files to change: `packaging/pi/image/*`, `packaging/release/*`,
  `packaging/distribution/*`, `internal/release/manifest.go`, and Pi docs.
