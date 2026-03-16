# v2.14.0 Plan: Raspberry Pi OS Lite Strategy and Receiver Image Deprecation

Milestone: `v2.14.0`

## Goal

Adopt one first-class Raspberry Pi path:

- flash official Raspberry Pi OS Lite
- install LoRaMapr Receiver from the existing package/repository path
- open local portal and pair

The custom LoRaMapr Receiver image path is deprecated for now.

## Current Pi Paths

Current repo/public surfaces show two Pi paths:

1. Pi appliance image (flash prebuilt receiver image)
2. Existing-OS package path (APT/.deb on Raspberry Pi OS)

The existing-OS path is already real and production-capable (APT + `.deb` +
systemd lifecycle). The appliance path adds release/build complexity and
duplicates user-facing install guidance.

## Target Product Story

Official Raspberry Pi guidance:

- install official Raspberry Pi OS Lite (64-bit recommended)
- configure Wi-Fi/hostname in Raspberry Pi Imager
- boot Pi
- run one canonical LoRaMapr install command path
- pair through local portal

Fallbacks remain available but secondary:

- manual `.deb`
- manual systemd-layout tarball

## Deprecation Meaning (Operational)

For the Receiver Image path, deprecation means:

- no active CI/release artifact generation
- no active publish/verify requirement
- no first-class reviewer test path
- no public recommendation in README/docs/release mapping

Internal scaffolding may remain in-repo for possible future reactivation, but
it is clearly marked paused/deprecated.

## v2.14.0 Implementation Landing Zones

Primary changes:

- release/build flow
  - `.github/workflows/release-artifacts.yml`
  - `packaging/release/build-artifacts.sh`
  - `packaging/release/README.md`
- distribution/publish/verify docs and checks
  - `packaging/distribution/README.md`
  - `packaging/distribution/verify.sh`
  - `docs/release-artifacts.md`
  - `docs/publish-guide.md`
- Pi install simplification and public docs
  - `packaging/linux/scripts/bootstrap-apt.sh` (new canonical helper)
  - `docs/linux-pi-distribution.md`
  - `README.md`
  - `docs/README.md`
  - `docs/reviewer-smoke-test.md`
- deprecation status notes
  - `docs/raspberry-pi-appliance.md`
  - `docs/pi-appliance.md`
  - `packaging/pi/README.md`
  - `packaging/pi/image/README.md`

Secondary wording/hints updates:

- `cmd/loramapr-receiverd/main.go`
- `internal/diagnostics/taxonomy.go`
- `internal/update/checker.go`
- affected tests/docs referencing appliance upgrade guidance

## Done Criteria

- Pi OS Lite + package install is the obvious recommended Pi path.
- Receiver image is clearly deprecated and not part of active release flow.
- Docs, release surfaces, and reviewer guidance align to one first-class Pi
  install story.
