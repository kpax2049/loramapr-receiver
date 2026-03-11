# v2.4.0 Plan: Update Channels and Upgrade Safety

Status: Implemented

Milestone: `v2.4.0`

## Scope

Receiver-side policy and implementation for:

- release channel model
- explicit version/channel/build reporting
- config/state/cloud-config upgrade compatibility
- thin update-status reasoning (no self-update)

Out of scope:

- automatic update download/install
- installer redesign
- cloud-side channel policy implementation details

## Current State Audit

What already exists:

- build metadata injection (`version`, `channel`, `commit`, optional `build_date`)
- runtime status exposure for version/channel/commit
- state schema migration and downgrade protection
- release manifest fragment generation and parsing helpers

Current gaps:

1. No fully explicit channel policy (stable vs beta semantics) in one place.
2. Build metadata is present but not consistently propagated across all local and
   cloud-facing surfaces.
3. Config schema migration/version marker behavior is implicit.
4. Cloud-config compatibility handling is not explicit when contract versions
   drift.
5. No runtime update-status reasoning (`current`, `outdated`, `channel_mismatch`,
   `unsupported`) surfaced to operators.

## Channel Policy (Target)

Supported channels:

- `stable`: production-grade releases for default install paths.
- `beta`: pre-production releases for early adopters and validation.
- `dev`: local/test builds and non-production experiments.

Channel expectations:

- stable receivers should track stable manifest recommendations.
- beta receivers may be ahead of stable and are expected to tolerate change.
- dev is not a support target for production upgrade guarantees.

## Version/Build Reporting Policy (Target)

Receiver must consistently report:

- semantic receiver version
- release channel
- build commit
- build date (if available)
- build identifier (if available)
- platform/arch
- install type/profile classification

These must be coherent across:

- runtime `/api/status`
- portal advanced/progress views
- diagnostics/support snapshot
- cloud heartbeat status payload metadata

## Upgrade Compatibility Policy (Target)

### Config

- explicit `config.schema_version` with migrations
- startup must reject config schema newer than runtime supports
- migration path must be documented and test-covered

### State

- explicit `state.schema_version` with additive migrations
- runtime must fail fast on newer unsupported state schema
- migration must preserve identity and credentials unless explicit reset policy
  requires clearing

### Cloud Config Contract

- receiver stores cloud-config version marker when provided by cloud
- receiver validates compatibility against supported cloud-config range
- incompatible cloud-config versions become explicit diagnosable blocked state

## Update-Status Reasoning Policy (Target)

Thin optional manifest-awareness only:

- no automatic install/update actions
- optional manifest fetch and evaluation
- offline-safe behavior when manifest is unavailable/stale

Update status categories:

- `current`
- `outdated`
- `channel_mismatch`
- `unsupported`
- `unknown`/`disabled` (manifest not available or checks disabled)

## Criteria Definitions (Target)

Receiver is considered:

- outdated: installed semantic version is lower than recommended manifest version
  for its channel.
- channel_mismatched: installed channel differs from manifest channel source used
  for evaluation.
- unsupported: installed version below configured minimum supported version, or no
  compatible artifact mapping for platform/arch.
- current: version/channel aligns with recommendation and compatibility checks.

## Implementation Landing Zones

Primary files to change in v2.4.0:

- build metadata model and injection:
  - `internal/buildinfo/*`
  - `packaging/release/build-artifacts.sh`
  - `cmd/loramapr-release-manifest/main.go`
  - `internal/release/manifest.go`
- status/runtime propagation:
  - `internal/status/model.go`
  - `internal/runtime/service.go`
  - `internal/runtime/service_test.go`
  - `internal/webportal/*`
  - `cmd/loramapr-receiverd/main.go`
  - `internal/diagnostics/snapshot.go`
- config/state/cloud-config migration hardening:
  - `internal/config/*`
  - `internal/state/*`
  - `internal/cloudclient/*`
  - `internal/pairing/manager.go`
- thin update-status module:
  - `internal/manifest/*` (reuse)
  - new `internal/update/*`
- docs:
  - `docs/version-channel-upgrades.md`
  - `docs/runtime-config-state.md`
  - `docs/diagnostics.md`
  - `docs/local-portal.md`
  - `docs/release-notes-v2.4.0.md`
  - `docs/reviewer-smoke-test.md`

## Concise Summary

- Current gaps: metadata propagation and upgrade/update policy are partially
  implemented but not fully explicit or productized.
- Target behavior: clear channel semantics, coherent build reporting, safe
  migration handling, and thin optional update-status reasoning.
- Implemented in this milestone:
  - centralized version/channel/build metadata propagation (`build_id` included)
  - config/state schema hardening (`config v2`, `state v3`) with migration guards
  - cloud config compatibility blocking/reporting
  - manifest-aware update-status reasoning (`current`, `outdated`,
    `channel_mismatch`, `unsupported`, `ahead`)
  - portal/diagnostics/status/heartbeat surface integration for upgrade visibility
