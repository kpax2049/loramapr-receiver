# LoRaMapr Receiver v2.4.0 (Update Channels and Upgrade Safety)

Release date: 2026-03-11

## Highlights

- Added explicit version/channel/build reporting across runtime surfaces:
  - `/api/status`
  - portal Progress/Advanced pages
  - heartbeat metadata
  - CLI `doctor`, `status`, and `support-snapshot`
- Expanded build metadata model with `build_id` and consistent release injection.
- Hardened config/state compatibility with explicit schema handling:
  - config schema `v2`
  - state schema `v3`
  - fail-fast protection for newer unsupported schemas
- Added persisted cloud-config version marker and compatibility enforcement.
- Added thin manifest-aware update-status reasoning (no auto-update):
  - `disabled`, `unknown`, `current`, `outdated`, `channel_mismatch`,
    `unsupported`, `ahead`
- Added update-status visibility in portal, diagnostics, and runtime state.

## Runtime Safety and Compatibility

- Runtime now blocks steady-state forwarding when cloud config contract version
  is incompatible and surfaces a clear diagnostic state.
- Upgrade compatibility errors now include explicit operator hints in CLI startup
  and diagnostics flows.
- Existing pairing/lifecycle behavior is preserved; no destructive migration
  behavior is introduced.

## Packaging and Release Metadata

- Release artifact pipeline now emits coherent build identifiers into:
  - binary build metadata
  - `release-metadata.json`
- Artifact naming/checksum/publication behavior remains stable for Linux/Pi GA
  paths.

## Scope Limits

- No automatic self-update/download/install behavior in this release.
- Update-status is informational and support-oriented only.
