# Version, Channel, and Upgrade Safety

This document defines receiver-side version/channel reporting and upgrade
compatibility behavior.

## Build Metadata Reporting

Receiver binaries report build metadata via `internal/buildinfo`:

- `version`
- `channel`
- `commit`
- optional `build_date`

Release builds inject these fields through release `ldflags` in
`packaging/release/build-artifacts.sh`.

## Runtime Status Exposure

Runtime status model now includes:

- `receiver_version`
- `release_channel`
- `build_commit`

These are visible in:

- local portal advanced details
- `/api/status`
- heartbeat status payload
- CLI `doctor` and `status` outputs

## Pairing and Heartbeat Metadata

Pairing activation metadata now includes release channel and commit in addition to
runtime version/platform/arch.

Heartbeat payload includes release channel and build commit under status metadata.

## State Upgrade Safety

State storage includes `schema_version` and migration support.

Current schema: `2`

On startup:

1. state is loaded
2. migrations are applied for older schemas
3. defaults are ensured
4. migrated state is persisted

Downgrade protection:

- if state schema is newer than supported by current binary, startup fails fast.

## Manifest Awareness Foundation

`internal/manifest` provides thin parsing and selection helpers for
`cloud-manifest.fragment.json` data:

- parse manifest fragment with tolerant unknown-field handling
- select artifact by platform/arch/kind with `recommended` preference

This is intentionally not a full auto-update engine.

## Known Limits

- no automatic self-update/download/install loop yet
- no signed delta update workflow yet
- cloud-side policy remains source of truth for upgrade channel rollout
