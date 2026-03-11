# Version, Channels, and Upgrade Safety

This document defines the receiver-side policy and behavior for release channel
semantics, build metadata reporting, and upgrade compatibility.

## Channel Model

Supported channels:

- `stable`: production-ready default channel for normal installs.
- `beta`: pre-production validation channel.
- `dev`: local/non-release builds and engineering validation.

Policy:

- stable installs should consume stable artifacts/manifests.
- beta installs may run ahead of stable and can report `ahead`.
- dev builds are not treated as production support targets.

## Build Metadata Model

Receiver build metadata is centralized in `internal/buildinfo` and release
builds inject:

- `version` (semantic version)
- `channel`
- `commit`
- `build_date` (optional; from `BUILD_DATE` or `SOURCE_DATE_EPOCH`)
- `build_id` (release build identifier; defaults to `<version>-<channel>[+commit]`)

Release injection is performed in:

- `packaging/release/build-artifacts.sh`

## Runtime Reporting Surfaces

A running receiver reports version/channel/build metadata consistently via:

- local status API: `GET /api/status`
- heartbeat status payload metadata
- local portal:
  - Progress (update/currentness and recommendations)
  - Advanced (version/channel/commit/build date/build id/platform/install type)
- CLI:
  - `loramapr-receiverd status`
  - `loramapr-receiverd doctor`
  - `loramapr-receiverd support-snapshot`

Reported core fields:

- semantic version
- release channel
- build commit
- build date (if present)
- build id (if present)
- platform/arch
- install type (`manual`, `linux-package`, `pi-appliance`, `windows-user`)

## Update-Status Reasoning (No Auto-Update)

Receiver includes thin, optional manifest-awareness in `internal/update`.

Behavior:

- no automatic download/install
- optional manifest fetch/evaluation (`update.enabled` + `update.manifest_url`)
- offline-safe fallback (`unknown` when manifest is unavailable)

Status codes:

- `disabled`: update checks disabled by config.
- `unknown`: insufficient information (manifest unset/fetch failed/unparseable).
- `current`: installed version matches recommendation.
- `outdated`: installed version is behind recommendation.
- `channel_mismatch`: installed channel differs from manifest channel.
- `unsupported`: below minimum supported version or no compatible artifact.
- `ahead`: installed version is newer than manifest recommendation.

These states are surfaced in portal, diagnostics outputs, and runtime status.

## Upgrade Compatibility Rules

### Local Config

- `config.schema_version` is explicit.
- Current supported config schema: `2`.
- Older config is migrated in-process.
- Config schema newer than runtime support is rejected at startup.

### Local State

- `state.schema_version` is explicit.
- Current supported state schema: `3`.
- Older state is migrated in-process.
- Newer state schema than runtime support fails fast (downgrade protection).

### Cloud Config Contract

- Cloud config version marker is persisted (`cloud.config_version`) when provided
  by cloud bootstrap/activation/heartbeat.
- Receiver currently accepts major version `1`.
- Unsupported cloud config version blocks steady-state forwarding and exposes
  explicit diagnostics (`cloud_config_incompatible` / `config_incompatible`).

## Compatibility Expectations

Upgrade-safe expectations for this milestone:

- restart after upgrade keeps installation identity and pairing state.
- migration is additive and non-destructive by default.
- explicit local reset/re-pair remains the recovery path when credentials or
  lifecycle state are invalidated.

Out of scope:

- automatic self-update orchestration
- rollback/downgrade tooling beyond schema guardrails
