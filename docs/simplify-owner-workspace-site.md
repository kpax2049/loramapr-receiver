# Receiver Simplification: Remove Required Owner/Workspace/Site Assumptions

Scope: `R-SIMPLIFY-1` planning note for `loramapr-receiver`.

## Goal

Ensure receiver onboarding and normal operation stay focused on:

1. install
2. pair
3. verify cloud reachability and node connection
4. confirm packet forwarding

The receiver must not depend on owner/workspace/site/location concepts for
runtime correctness.

## Current Assumptions Found

Observed assumptions were mostly wording-level, not hard runtime blockers:

- Portal next-step and troubleshooting text referenced household/team/grouping
  context as if it were part of normal setup.
- Progress/Advanced pages showed cloud site/group labels as first-class rows,
  even though they are optional metadata.
- CLI `doctor` output used "grouping hints" wording.
- Pairing lifecycle docs listed `ownerId` in activate response credentials
  without marking it as optional metadata.
- Support/reviewer docs emphasized site/group labels in identity checks.

## Simplification Target

### Required behavior

- Pairing/activation remain valid with no owner/workspace/site/group input from
  local operator.
- Portal language describes a direct pairing flow with no required organization
  concepts.
- Status and diagnostics continue to expose optional identity labels if cloud
  provides them, but absence is non-failure.

### Optional metadata that may remain

- `cloud_receiver_label`
- `cloud_site_label`
- `cloud_group_label`
- internal `owner_id` persistence when cloud includes it

These fields are support/context hints only and must remain non-blocking.

## Implementation Landing Zones

Runtime and UI:

- `internal/webportal/server.go`
- `internal/webportal/templates/progress.tmpl`
- `internal/webportal/templates/advanced.tmpl`
- `cmd/loramapr-receiverd/main.go`

Docs:

- `docs/local-portal.md`
- `docs/diagnostics.md`
- `docs/pairing-lifecycle.md`
- `docs/runtime-config-state.md`
- `docs/support-operations-workflow.md`
- `docs/reviewer-smoke-test.md`

## Out of Scope

- No runtime architecture redesign.
- No cloud contract redesign beyond treating optional metadata as optional.
- No removal of existing optional identity fields from status payloads.

## Acceptance Checks

- Portal and CLI wording do not imply owner/workspace/site are required.
- Pairing and steady-state behavior are unchanged.
- Docs describe optional metadata clearly and consistently.
