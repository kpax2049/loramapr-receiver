# LoRaMapr Receiver v2.1.0 (Linux/Pi Existing-OS GA)

Date: 2026-03-11

## Highlights

- Promoted Debian-family existing-OS install path to GA scope.
- Added native `.deb` package generation for:
  - `linux/amd64`
  - `linux/arm64`
  - `linux/armv7` (`armhf` package architecture)
- Integrated `.deb` artifacts into release outputs, checksums, and manifest
  metadata.
- Implemented signed APT repository publication scripts:
  - package indexes (`Packages`, `Packages.gz`)
  - release metadata (`Release`, `InRelease`, `Release.gpg`)
  - repository key export (`loramapr-archive-keyring.asc/.gpg`)
- Wired APT publish/verify flow into distribution scripts and CI validation.
- Hardened package lifecycle semantics for install/upgrade/remove/purge:
  - config/state preservation on remove
  - explicit purge behavior
  - service lifecycle handling
  - practical tarball-to-package migration handling
- Added lifecycle validation script and lifecycle policy documentation.
- Updated smoke-test guide for Debian-family end-to-end reviewer validation.

## Supported Production Path

- Debian-family package install via APT repository (`.deb` + signed metadata)
- Debian, Ubuntu-family, Raspberry Pi OS

## Fallback / Advanced Path

- Linux systemd layout tarballs remain available for manual advanced installs.

## Not in Scope for v2.1.0

- Pi flash-image/appliance path changes
- macOS notarized installer pipeline
- Windows signed installer pipeline
