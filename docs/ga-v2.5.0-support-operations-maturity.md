# v2.5.0 Plan: Support and Operations Maturity

Status: Implemented

Milestone: `v2.5.0`

## Scope

Receiver-side support maturity and local operations visibility for:

- support bundle/export hardening
- redaction guarantees
- coarse operational status checks
- portal/CLI guidance for common field failures
- support workflow documentation

Out of scope:

- runtime architecture redesign
- external telemetry stack/agent integration
- secret-bearing debug dump exports

## Current State Audit

What already exists:

- runtime status model and `/api/status`
- portal setup/progress/troubleshooting pages
- `doctor`, `status`, and `support-snapshot` commands
- coarse diagnostics taxonomy and support-safe hints
- lifecycle/update-state signaling

Current gaps:

1. Support export lacks explicit operational-check summary for fast triage.
2. Support export does not clearly capture local service visibility from the
   running portal/status API when available.
3. Config/state compatibility failures are not modeled as first-class support
   taxonomy states.
4. Coarse ops-state wording can be more product-facing for common field cases
   (offline cloud, node missing, paired but no forwarding, outdated/unsupported).
5. Support workflow docs are spread across files and need tighter receiver-side
   runbook guidance.

## Support Bundle Policy (Target)

Primary export path:

- CLI `support-snapshot` JSON (support bundle format for this milestone)

Support bundle contents:

- runtime metadata:
  - version/channel/commit/build date/build id
  - platform/arch/install type
- config/state markers:
  - config schema version
  - state schema version
  - runtime profile, config path, state path
- lifecycle/update status:
  - pairing phase + last change
  - update status/summary/recommendation
  - cloud-config version marker
- connectivity and node visibility:
  - cloud reachability probe
  - local network probe
  - meshtastic detection/connection summary
- recent coarse errors and failure code/hint
- operational checks:
  - service healthy/running
  - pairing authorized
  - cloud reachable
  - node connected
  - forwarding recently active
  - update supportability state

## Redaction Policy (Target)

Never include raw secret values in portal/CLI/export:

- pairing code
- activation token
- ingest API secret
- durable credential secret material

Allow only support-safe booleans/metadata:

- presence booleans for secret fields
- credential identifiers where already non-secret (if needed later)

## Support-Safe Ops Visibility (Target)

Coarse operational checks should be stable and actionable:

- `ok`, `warn`, `fail`, `unknown` per check
- overall status:
  - `ok`
  - `degraded`
  - `blocked`

Checks and taxonomy should align with cloud-side support vocabulary where
possible.

## Surface Split Policy (Target)

Portal (human first):

- current state + practical next action
- troubleshooting hints for common failures
- no raw dump-style payloads

CLI `doctor` / `status`:

- machine-readable JSON and concise human-readable operational checks
- direct field support for support tickets/escalation

Support export (`support-snapshot`):

- canonical redacted bundle for attachments/escalation
- richer than portal; safer than raw logs/state dumps

## Implementation Landing Zones

Primary files changed for v2.5.0:

- diagnostics core:
  - `internal/diagnostics/snapshot.go`
  - `internal/diagnostics/taxonomy.go`
  - `internal/diagnostics/*_test.go`
  - new ops/probe helpers in `internal/diagnostics/`
- CLI surfaces:
  - `cmd/loramapr-receiverd/main.go`
- portal guidance:
  - `internal/webportal/server.go`
  - `internal/webportal/templates/*.tmpl`
  - `internal/webportal/server_test.go`
- docs:
  - `docs/diagnostics.md`
  - `docs/local-portal.md`
  - `docs/reviewer-smoke-test.md`
  - `docs/release-notes-v2.5.0.md`
  - index updates in `docs/README.md`, `docs/release-notes.md`, `README.md`

## Concise Summary

- Current support/ops gaps: export and local triage are useful but still too
  fragmented for fast field support.
- Target behavior: a consistent redacted support bundle plus coarse operational
  checks aligned across portal, CLI, and diagnostics taxonomy.
- Next implementation focus: add operational check primitives, enrich
  support-snapshot contents/redaction coverage, and tighten field troubleshooting
  docs/runbook guidance.
