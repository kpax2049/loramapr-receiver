# LoRaMapr Receiver v1.1.0 (Bridge Batch)

Date: 2026-03-11

## Highlights

- Added machine-consumable `cloud-manifest.fragment.json` and `release-metadata.json`
  generation as part of release artifacts.
- Added Linux/Pi signed static distribution skeleton with publish and verify
  scripts, channel index metadata, and CI validation workflow.
- Added first-run diagnostics taxonomy with actionable failure states surfaced in
  runtime status, local portal, and CLI.
- Added `support-snapshot` command for redacted support bundle export.
- Added explicit build metadata reporting (`version`, `channel`, `commit`) across
  status, heartbeat, and diagnostics surfaces.
- Added state schema versioning and migration handling for upgrade safety.
- Added thin manifest-awareness parsing/selection package for future update-check
  integration.

## Linux/Pi-first Scope

This phase is Linux/Pi-first and publication-ready for receiver artifacts and
signed metadata workflows.

## Deferred Work

- Full native APT `.deb` repository publication path
- Full macOS notarized installer pipeline
- Full Windows signed installer pipeline
- Automatic self-update engine
