# v2.7.0 Plan: Public Launch Hardening and Docs Polish

Status: Implemented

Milestone: `v2.7.0`

## Goal

Make LoRaMapr Receiver understandable and trustworthy for first-time public users
without requiring internal implementation context.

This milestone is documentation and wording hardening, not a runtime feature
expansion.

## Supported Public Install Paths (to Emphasize)

1. Raspberry Pi Appliance (recommended for most first-time users)
   - flash image
   - boot on LAN
   - open local portal
   - pair with code from LoRaMapr Cloud
2. Existing Debian-family Linux / Raspberry Pi OS install
   - install `loramapr-receiver` package from signed APT repository
   - service starts at boot
   - open local portal and pair

Fallback/advanced path:

- manual systemd-layout tarball install (kept documented but clearly secondary)

## Terminology Policy (Must Stay Consistent)

Use these terms consistently across README, docs, portal, and diagnostics:

- `LoRaMapr Receiver`: product runtime
- `local portal`: human setup/status UI at `http://<host>:8080`
- `pairing code`: short-lived code from LoRaMapr Cloud
- `pairing-ready`: receiver installed and waiting for pairing code
- `attention state`: `none`, `info`, `action_required`, `urgent`
- `support snapshot`: redacted diagnostics export from `support-snapshot`
- `reset and re-pair`: local recovery action for revoked/replaced/disabled identity

Avoid using internal-only wording as primary user guidance, including:

- implementation package names as user instructions
- internal milestone references in end-user setup paths
- overly protocol-specific terms when simpler user wording is sufficient

## Public Docs That Must Be Coherent

Required launch-facing surfaces:

- root `README.md` (product overview + install path selection)
- `docs/README.md` (clear navigation, not just flat catalog)
- `docs/raspberry-pi-appliance.md` (recommended path)
- `docs/linux-pi-distribution.md` (existing-OS path)
- `docs/local-portal.md` (where to click/what states mean)
- `docs/diagnostics.md` + `docs/support-operations-workflow.md` (support and failure recovery)
- `docs/release-artifacts.md` (download and artifact naming clarity)
- `docs/reviewer-smoke-test.md` (public launch verification)

## Local Portal and Diagnostics Wording Polish Targets

Portal wording should:

- show plain-language next actions first
- keep raw codes visible but secondary
- explain attention states in user terms
- keep troubleshooting actionable without requiring shell access

Diagnostics/help text should:

- prioritize "what to do next"
- show minimal command set for support collection
- keep redaction expectations explicit and simple

## Reviewer Smoke Tests Required for Public Launch

Reviewer guide should cover both public install paths:

1. Pi appliance: flash -> boot -> portal -> pairing-ready
2. Existing Linux/Pi OS: apt install -> service start -> portal -> pairing-ready
3. Basic failure triage:
   - cloud unreachable
   - node missing/not connected
   - paired but not forwarding
4. Support bundle export and redaction verification
5. Lifecycle recovery (`reset and re-pair`) and outdated/unsupported basics

## Current Gaps Identified

1. README/docs navigation is still maintainer-heavy for first-time users.
2. Install docs are technically complete but need clearer "choose this path" language.
3. Portal wording surfaces internal status labels without enough plain-language framing.
4. Troubleshooting and reviewer guidance is split and does not lead with both install paths.
5. Release artifact docs lack a simple external-user "what do I download" view.

## Implementation Landing Zones

Primary files updated in this milestone:

- product/readme surfaces:
  - `README.md`
  - `docs/README.md`
- install path docs:
  - `docs/raspberry-pi-appliance.md`
  - `docs/linux-pi-distribution.md`
- portal and diagnostics docs:
  - `docs/local-portal.md`
  - `docs/diagnostics.md`
  - `docs/support-operations-workflow.md`
- release/download clarity:
  - `docs/release-artifacts.md`
- local portal text polish:
  - `internal/webportal/server.go`
  - `internal/webportal/templates/*.tmpl`
  - `internal/webportal/server_test.go` (as needed)
- final launch docs:
  - `docs/reviewer-smoke-test.md`
  - `docs/release-notes-v2.7.0.md`
  - `docs/release-notes.md`

## Concise Summary

- Launch-readiness gaps: user-facing flow is not yet as direct as product launch needs.
- Target polish: first-time users can choose install path, pair, and troubleshoot
  with minimal ambiguity.
- Next changes: tighten README/docs flow, simplify portal wording, and consolidate
  reviewer/support validation steps.
