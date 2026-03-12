# v2.8.0 Plan: Multi-Receiver / Household / Team Operations

Status: Implemented

Milestone: `v2.8.0`

## Goal

Make LoRaMapr Receiver identity and local guidance clear when multiple receivers
exist under the same account/site/team.

This milestone improves identity reporting, local wording, and docs. It does not
add local cross-device coordination or move ownership/grouping logic out of cloud.

## Receiver-Side Identity Expectations

Each receiver reports stable support-safe identity hints:

- `installation.id` (local durable identity anchor)
- `installation.local_name` (operator-facing local name hint)
- `installation.hostname` (runtime host hint)
- cloud receiver identity hints when available:
  - `cloud.receiver_id`
  - `cloud.receiver_label`
  - `cloud.site_label`
  - `cloud.group_label`
- install/runtime context:
  - install type/path profile
  - platform/arch
  - version/channel/build metadata

These hints help humans and cloud support distinguish similar deployments.

## Naming and Display Policy

Local name precedence:

1. `runtime.local_name` config override
2. previously persisted `installation.local_name`
3. derived default from hostname/install type + installation suffix

Display guidance:

- portal and diagnostics should show local and cloud identity hints side-by-side
- wording should stay support-safe and human-readable
- cloud naming remains authoritative when present

## Cloud-Managed vs Receiver-Managed Boundaries

Cloud-managed (source of truth):

- ownership/account/team membership
- site/group relationships and policy
- canonical lifecycle/replacement decisions

Receiver-managed:

- local identity hints and install context
- clear local explanation of lifecycle/replacement outcomes
- support snapshot export of identity context

## Install Layout and Pairing Flow Impact

- no install-path redesign
- no pairing flow redesign
- existing Linux package + Pi appliance paths remain supported
- tarball/manual path remains fallback/advanced

## Current Gaps (Before v2.8 Work)

1. Single-receiver assumptions in portal and diagnostics wording.
2. Missing explicit local name/hostname identity hints in some local surfaces.
3. Limited reflection of cloud receiver/site/group labels in local views.
4. Reviewer guidance not explicit about multi-receiver coexistence/replacement.

## Implementation Landing Zones

- identity/state/config:
  - `internal/config/config.go`
  - `internal/state/state.go`
  - `internal/status/model.go`
  - `internal/runtime/identity.go`
  - `internal/runtime/service.go`
- cloud/pairing propagation:
  - `internal/cloudclient/client.go`
  - `internal/pairing/manager.go`
- local UX/diagnostics:
  - `internal/webportal/server.go`
  - `internal/webportal/templates/*.tmpl`
  - `cmd/loramapr-receiverd/main.go`
  - `internal/diagnostics/snapshot.go`
- docs/review:
  - `docs/local-portal.md`
  - `docs/runtime-config-state.md`
  - `docs/diagnostics.md`
  - `docs/reviewer-smoke-test.md`
  - `docs/release-notes-v2.8.0.md`

## Concise Summary

- Single-receiver assumptions are reduced by introducing explicit identity hints.
- Receiver now reports local + cloud identity context needed for multi-receiver
  support flows.
- Cloud keeps ownership/grouping authority; receiver stays lightweight and
  support-oriented.
