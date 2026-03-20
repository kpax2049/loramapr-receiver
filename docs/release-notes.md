# LoRaMapr Receiver Release Notes

For passive-serial safety hotfix notes targeting `v2.16.8`, see
`docs/release-notes-v2.16.8.md`.

For serial-stability and portal auto-refresh notes targeting `v2.16.7`, see
`docs/release-notes-v2.16.7.md`.

For serial-hangup/runtime hardening notes targeting `v2.16.3`, see
`docs/release-notes-v2.16.3.md`.

For Linux/Pi one-click hardening notes targeting `v2.16.0`, see
`docs/release-notes-v2.16.0.md`.

For Pages-backed APT hosting notes targeting `v2.15.0`, see
`docs/release-notes-v2.15.0.md`.

For Raspberry Pi OS Lite strategy shift notes targeting `v2.14.0`, see
`docs/release-notes-v2.14.0.md`.

For Meshtastic field-node pairing data exposure notes targeting `v2.13.0`, see
`docs/release-notes-v2.13.0.md`.

For receiver-side onboarding simplification cleanup notes (owner/workspace/site
assumptions), see `docs/release-notes-simplification.md`.

For Embedded Home Auto Session Milestone 4 cloud-managed config notes targeting
`v2.12.0`, see `docs/release-notes-v2.12.0.md`.

For Embedded Home Auto Session Milestone 3 production-control notes targeting
`v2.11.0`, see `docs/release-notes-v2.11.0.md`.

For Embedded Home Auto Session Milestone 2 correctness/recovery notes targeting
`v2.10.0`, see `docs/release-notes-v2.10.0.md`.

For Embedded Home Auto Session Milestone 1 notes targeting `v2.9.0`, see
`docs/release-notes-v2.9.0.md`.

For Multi-Receiver / Household / Team Operations notes targeting `v2.8.0`, see
`docs/release-notes-v2.8.0.md`.

For Public Launch Hardening and Docs Polish notes targeting `v2.7.0`, see
`docs/release-notes-v2.7.0.md`.

For Operational Automation and Notifications notes targeting `v2.6.0`, see
`docs/release-notes-v2.6.0.md`.

For Support and Operations Maturity notes targeting `v2.5.0`, see
`docs/release-notes-v2.5.0.md`.

For Update Channels and Upgrade Safety notes targeting `v2.4.0`, see
`docs/release-notes-v2.4.0.md`.

For Receiver Lifecycle Management notes targeting `v2.3.0`, see
`docs/release-notes-v2.3.0.md`.

For Raspberry Pi Appliance GA notes targeting `v2.2.0`, see
`docs/release-notes-v2.2.0.md`.

For Linux/Pi Existing-OS GA notes targeting `v2.1.0`, see
`docs/release-notes-v2.1.0.md`.

For bridge-batch release notes targeting `v1.1.0`, see
`docs/release-notes-v1.1.0.md`.

## M2.1.0 Runtime Alpha

Date: 2026-03-10

Highlights:

- Introduced standalone `loramapr-receiverd` service runtime with clean startup/shutdown lifecycle.
- Added persistent config and state model for installation identity, pairing lifecycle, and cloud endpoints.
- Added structured logging and shared runtime status model.
- Added embedded local setup portal with status and pairing code entry.
- Added receiver-side pairing/bootstrap state machine aligned with `loramapr-cloud` bootstrap/activate flow.

Scope limits:

- No native installers yet.
- Meshtastic integration at this stage was runtime-boundary and lifecycle scaffolding only.

## M2.2.0 Meshtastic Beta

Date: 2026-03-10

Highlights:

- Added Meshtastic adapter detection and connection lifecycle:
  - `not_present`, `detected`, `connecting`, `connected`, `degraded`.
- Added node/status extraction for onboarding visibility.
- Added packet/event normalization boundary for backend ingest compatibility.
- Added steady-state cloud loop:
  - ingest posting
  - heartbeat reporting
  - coarse receiver/node status reporting
- Added retry/backoff queue behavior for temporary connectivity outages.

Scope limits:

- Normalization currently targets internal receiver ingest contract; deeper protocol-specific enrichments remain follow-up work.

## M2.3.0 Packaging Beta

Date: 2026-03-10

Highlights:

- Added Linux-first service/install model with command modes:
  - `run`, `install`, `uninstall`, `doctor`, `status`
- Added systemd install assets and install/uninstall helpers.
- Added release artifact pipeline skeleton with multi-platform matrix and checksums.
- Added Raspberry Pi appliance/image scaffolding with appliance profile defaults and pi-gen stage prep.
- Added artifact mapping docs for cloud download manifest integration.

Scope limits:

- macOS notarization and Windows installer remain scaffolded placeholders.
- Pi image path is scaffolding-driven and intended for iterative hardening.
