# v2.1.0 GA Plan: Linux/Pi Existing-OS (Debian-family)

Status: Draft for implementation
Date: 2026-03-11
Milestone: `v2.1.0`

## Goal

Make installation on existing Debian-family Linux systems, including Raspberry Pi
OS, the first production-grade supported path for `loramapr-receiverd`.

This milestone explicitly excludes the Pi flash-image/appliance path.

## Current Install/Distribution State (Audited)

### Runtime install/service path (already real)

- `loramapr-receiverd install/uninstall` command path exists and is usable.
- Linux systemd unit asset exists and is wired into current layout.
- Current layout assumptions are documented and used by packaging scripts.

Primary files:

- `cmd/loramapr-receiverd/main.go`
- `internal/install/layout.go`
- `internal/install/linux.go`
- `packaging/linux/systemd/loramapr-receiverd.service`
- `packaging/linux/scripts/install.sh`
- `packaging/linux/scripts/uninstall.sh`

### Release outputs (already real)

- Multi-arch Linux binaries and systemd-layout tarballs are produced.
- Checksums and cloud manifest fragment outputs are produced.

Primary files:

- `packaging/release/build-artifacts.sh`
- `packaging/release/targets.json`
- `internal/release/manifest.go`
- `docs/release-artifacts.md`

### Publish/signing skeleton (already real, non-APT)

- Signed static publication skeleton exists for current artifact set.
- Not yet a native APT repository with package indexes/Release metadata.

Primary files:

- `packaging/distribution/publish.sh`
- `packaging/distribution/verify.sh`
- `packaging/distribution/README.md`

## Gaps to Debian-family GA

1. No native `.deb` package outputs yet.
2. No APT repository structure (`dists/`, `pool/`, `Packages`, `Release`, signatures).
3. No package lifecycle policy implementation (`install/upgrade/remove/purge`) in package metadata/maintainer scripts.
4. Existing tarball/systemd path is usable but should become fallback/advanced once `.deb`/APT path is in place.

## v2.1.0 Target Output

### Package formats

- Primary GA format: `.deb`
- Keep current service-layout tarball outputs as fallback/advanced for this milestone.

### Supported architectures

- `amd64`
- `arm64`
- `armv7`/`armhf` (Debian-family mapping for Raspberry Pi OS 32-bit path)

### Install layout (package path)

- binary: `/usr/bin/loramapr-receiverd`
- unit: `/lib/systemd/system/loramapr-receiverd.service`
- config: `/etc/loramapr/receiver.json`
- state: `/var/lib/loramapr/receiver-state.json`
- logs: `/var/log/loramapr` (or journald)

### Install/upgrade/remove expectations

- fresh install: service enabled and started (unless policy-disabled by admin)
- upgrade: preserve config/state, restart service safely
- remove: package removed, config/state policy explicitly documented
- purge: explicit policy documented (what remains vs deleted)

### Repository publication model

- Signed APT repository for Debian-family users:
  - `dists/<channel>/...`
  - `pool/main/...`
  - signed release metadata
- Stable channel required; beta channel optional if low complexity.

### Fallback/advanced path

- Existing manual tarball/systemd extraction path remains available and documented
  as fallback/advanced for v2.1.0.

## Implementation Landing Zones (Next Prompts)

### R-GA2 (.deb packaging)

Add/modify:

- `packaging/debian/` (new): control templates, maintainer scripts, packaging helpers
- `packaging/release/build-artifacts.sh` (integrate `.deb` outputs)
- `docs/release-artifacts.md` and `docs/linux-pi-distribution.md` (artifact naming/content/lifecycle)
- optional validation script/workflow for `.deb` structure checks

Commands to add/use:

- package build helper under `packaging/debian/` (invoked from release flow)

### R-GA3 (APT publish)

Add/modify:

- `packaging/distribution/apt/` (repo generation + signing scripts)
- `packaging/distribution/publish.sh` (or wrapper) to include APT output path
- `packaging/distribution/verify.sh` to validate APT metadata/signatures
- docs for apt install flow and key setup

Commands to add/use:

- apt repo generation command(s)
- metadata signing command(s)

### R-GA4 (lifecycle hardening)

Add/modify:

- package maintainer scripts behavior
- lifecycle docs for install/upgrade/remove/reinstall
- smoke-test scripts validating package lifecycle behavior
- diagnostics docs with lifecycle troubleshooting notes

### R-GA5 (final integration)

Add/modify:

- finalize docs coherence
- release notes for v2.1.0
- reviewer smoke tests for Debian-family end-to-end install and pairing-ready state

## Acceptance for This Plan Step

- Debian-family GA target is explicit and repo-grounded.
- Scope excludes Pi image/appliance for this milestone.
- Next prompts have clear file/command landing zones.
