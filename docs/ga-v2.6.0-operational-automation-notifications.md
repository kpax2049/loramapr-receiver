# v2.6.0 Plan: Operational Automation and Notifications

Status: Implemented

Milestone: `v2.6.0`

## Scope

Receiver-side alignment for cloud automation/notification workflows by improving:

- stable support-safe status signals
- remediation hints for local and cloud attention workflows
- local attention visibility in portal and CLI surfaces
- docs and reviewer guidance for automation-aligned operations

Out of scope:

- autonomous local remediation agent
- remote command execution from cloud
- heavy monitoring/alerting stack dependencies
- changes to supported install paths

## Current State Audit

What already existed:

- coarse failure taxonomy (`internal/diagnostics/taxonomy.go`)
- lifecycle and update-status modeling in runtime/status
- portal troubleshooting and operational checks
- support snapshot export and redaction model
- heartbeat/status payload path to cloud backend

Gaps identified for this milestone:

1. Local and cloud-facing status needed a consistent attention-level abstraction.
2. Remediation hints were present but not uniformly mapped across portal, diagnostics, and heartbeat status payloads.
3. Portal did not emphasize attention states prominently for nearby operators.
4. Docs did not explicitly define receiver role boundaries for automation/notification workflows.

## Receiver Role in Attention/Automation

Receiver is a signal source and local guidance surface, not an autonomous manager.

Receiver responsibilities:

- emit coarse, stable operational state for cloud-side attention classification
- emit support-safe remediation hints with no credentials
- surface local attention state in portal/doctor output even when cloud is unreachable
- preserve retry/backoff and offline-tolerant behavior

Explicitly out of scope:

- self-directed account policy changes
- automatic credential rotation without explicit pairing flow
- local scripts/actions triggered by remote commands

## Attention State Mapping

Receiver derives an `Attention` model from failure taxonomy + operational checks.

Attention states:

- `none`
- `info`
- `action_required`
- `urgent`

Attention categories:

- `pairing`
- `connectivity`
- `authorization`
- `lifecycle`
- `node`
- `forwarding`
- `version`
- `compatibility`
- `service`

Representative mapping used by runtime/portal/diagnostics:

- `pairing_not_completed`, `pairing_code_invalid`, `pairing_code_expired`, `activation_failed` -> `pairing` / `action_required`
- `cloud_unreachable`, `network_unavailable`, `portal_unavailable` -> `connectivity` / `action_required`
- `receiver_auth_invalid` -> `authorization` / `urgent`
- `receiver_credential_revoked`, `receiver_disabled`, `receiver_replaced` -> `lifecycle` / `urgent`
- `no_serial_device_detected`, `node_detected_not_connected` -> `node` / `action_required`
- `events_not_forwarding` -> `forwarding` / `action_required`
- `receiver_outdated` -> `version` / `action_required`
- `receiver_version_unsupported` -> `version` / `urgent`
- `local_schema_incompatible`, `cloud_config_incompatible` -> `compatibility` / `urgent`

## Surface Split Policy

Portal (human-first local operator):

- show current attention state/category/code
- show concise next-step guidance
- preserve existing setup/status/troubleshooting UX

CLI and support bundle:

- expose same attention fields in `doctor`, `status`, and `support-snapshot`
- include operational summary + checks for triage context
- keep outputs secret-free and shareable

Cloud-facing status payloads (heartbeat/status):

- include support-safe attention and operational summary fields
- avoid raw secrets and account-sensitive values

## Acceptable Automatic Local Behavior

Allowed:

- existing retry/backoff for heartbeat + ingest
- local state transitions and readiness updates
- recalculation of attention and operational summaries

Not allowed in this milestone:

- auto-reset/re-pair without explicit user action
- auto-install/update/rollback workflows
- remote-triggered local shell/actions

## Implementation Landing Zones

Primary files changed for v2.6.0:

- attention model and mapping:
  - `internal/diagnostics/attention.go`
  - `internal/diagnostics/attention_test.go`
- runtime/status propagation:
  - `internal/status/model.go`
  - `internal/runtime/service.go`
  - `internal/runtime/service_test.go`
- support snapshot alignment:
  - `internal/diagnostics/snapshot.go`
  - `internal/diagnostics/snapshot_test.go`
- local portal attention visibility:
  - `internal/webportal/server.go`
  - `internal/webportal/templates/*.tmpl`
  - `internal/webportal/server_test.go`
- CLI attention output:
  - `cmd/loramapr-receiverd/main.go`
- docs:
  - `docs/diagnostics.md`
  - `docs/local-portal.md`
  - `docs/support-operations-workflow.md`
  - `docs/reviewer-smoke-test.md`
  - `docs/release-notes-v2.6.0.md`
  - index updates in `docs/README.md`, `docs/release-notes.md`, `README.md`

## Concise Summary

- Current gaps: attention/remediation signals were present but not consistently productized across local and cloud-facing surfaces.
- Target receiver role: provide stable support-safe attention signals and local guidance, while remaining lightweight and non-autonomous.
- Next landing zones: maintain centralized attention mapping and keep portal/CLI/support bundle/heartbeat fields aligned to the same coarse taxonomy.
