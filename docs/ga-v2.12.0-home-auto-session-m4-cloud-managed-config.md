# v2.12.0 Plan: Embedded Home Auto Session Milestone 4 (Cloud-Managed Config)

Status: Implemented

Milestone: `v2.12.0`

## Goal

Add minimal, safe cloud-managed Home Auto Session config application on receiver:

- fetch through receiver-authenticated cloud runtime contract
- validate before apply
- apply deterministic precedence
- persist/report source + version + apply result
- keep local fallback explicit and support-safe

Milestone 4 does not add new policy features.

## Current Local-Only Assumptions (Before M4)

1. Home Auto Session effective policy is always local config.
2. Receiver does not track cloud-vs-local effective source/version.
3. Portal and diagnostics do not show cloud-managed config apply state.
4. Runtime has no centralized policy-fetch/apply pipeline for Home Auto Session.

## Target Receiver-Side Model

## 1) Fetch / Apply Contract

Primary contract for M4 is an optional extension on heartbeat ack:

- `ReceiverHeartbeatAck.homeAutoSessionConfig` (optional, nullable)

If the cloud runtime contract does not provide this field, receiver remains
fully functional and uses local fallback policy.

Expected cloud payload shape (receiver-side contract adapter):

- `version` (string)
- `enabled` (bool)
- `mode` (`off|observe|control`)
- `home.lat`, `home.lon`, `home.radius_m`
- `tracked_node_ids[]`
- `start_debounce`, `stop_debounce`, `idle_stop_timeout`
- `startup_reconcile`
- optional session text templates and endpoint overrides

## 2) Precedence Rules

Deterministic precedence for effective Home Auto Session policy:

1. valid cloud-managed config (if present) wins
2. otherwise local fallback config is applied

No implicit deep merge in M4. Effective policy is one concrete config object.

## 3) Version / Apply Tracking

Receiver tracks and exposes at minimum:

- `effective_config_source` (`cloud` or `local_fallback`)
- `effective_config_version`
- `cloud_config_present`
- `last_fetched_config_version`
- `last_applied_config_version`
- `last_config_apply_result`
- `last_config_apply_error`

These values persist in local state and are surfaced in status/portal/doctor/
support snapshot.

## 4) Validation and Safe Apply

- Cloud payload is mapped to local Home Auto Session config model.
- Receiver validates mapped config before apply.
- Invalid cloud payload is not applied.
- On invalid cloud payload, receiver keeps/uses local fallback and reports
  explicit apply error/result.

## 5) Fallback / Degraded Behavior

Required behavior:

- no cloud config present: use local fallback
- valid cloud config present: apply cloud-managed policy
- cloud config disables feature: apply disabled effective config
- invalid cloud config: do not apply; use local fallback
- cloud unavailable: keep last effective policy and report fetch/apply status

Packet forwarding remains primary and unaffected.

## 6) Portal and Diagnostics Expectations

Home Auto Session portal section must show:

- effective source
- effective version
- cloud config presence
- last fetch/apply result
- apply error (if any)
- desired policy mode/enabled vs runtime state (plain language)

Diagnostics (`doctor`, `support-snapshot`, `/api/status`) must expose the same
support-safe model consistently.

## 7) Milestone Limitations

Out of scope for M4:

- cloud-managed advanced policy/rules expansion
- remote execution or autonomous remediation
- full bidirectional config sync protocol
- replacing local fallback/support surfaces

## Files/Areas to Change Next

Cloud/runtime:

- `internal/cloudclient/client.go`
- `internal/cloudclient/client_test.go`
- `internal/runtime/service.go`
- `internal/runtime/service_test.go`

Home Auto Session + status/state:

- `internal/homeautosession/module.go`
- `internal/homeautosession/module_test.go`
- `internal/state/state.go`
- `internal/state/state_test.go`
- `internal/status/model.go`
- `internal/status/model_test.go`

Portal/diagnostics/CLI:

- `internal/webportal/server.go`
- `internal/webportal/templates/home_auto_session.tmpl`
- `internal/webportal/server_test.go`
- `internal/diagnostics/snapshot.go`
- `internal/diagnostics/snapshot_test.go`
- `cmd/loramapr-receiverd/main.go`

Docs/release:

- `docs/home-auto-session.md`
- `docs/local-portal.md`
- `docs/runtime-config-state.md`
- `docs/diagnostics.md`
- `docs/reviewer-smoke-test.md`
- `docs/release-notes-v2.12.0.md` (new)
- `docs/release-notes.md`
- `docs/README.md`

## Concise Summary

- Current assumption is local-only policy.
- M4 adds a safe cloud-managed policy application layer with explicit
  precedence and persisted source/version/apply telemetry.
- Fallback remains explicit and product-safe when cloud config is missing,
  invalid, or unavailable.
