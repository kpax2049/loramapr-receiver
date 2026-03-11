# LoRaMapr Receiver Release Notes

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
