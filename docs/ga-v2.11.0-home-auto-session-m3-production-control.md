# v2.11.0 Plan: Embedded Home Auto Session Milestone 3 (Production Control Model)

Status: Implemented

Milestone: `v2.11.0`

## Goal

Make Home Auto Session operationally safe and understandable for real Linux/Pi
users by finalizing control semantics, conflict handling, receiver/cloud
reconciliation expectations, and support-facing UX/diagnostics.

Milestone 3 does not expand policy scope.

## Scope Constraints

Preserved policy model:

- one home geofence
- explicit tracked node IDs
- one active auto session per receiver
- packet forwarding remains primary and non-blocking

Out of scope:

- cloud-managed Home Auto Session configuration
- multi-session orchestration
- advanced policy/rules engine

## Current Production-Safety Gaps (Before M3)

1. Conflict responses from cloud (already-active/already-closed/mismatch) are not
   consistently mapped into stable production control semantics.
2. Lifecycle conflict classes (revoked/disabled/replaced) are visible broadly in
   receiver runtime, but Home Auto Session control flow still reports them too
   generically.
3. Portal and diagnostics show rich state internals but need clearer
   operator-facing “last action + result + source-of-truth” clarity.
4. Inclusion/default-off behavior exists in configs, but docs need stronger
   product-level wording across both supported install paths.

## Target Production Control Model

### 1) Explicit Enablement and Default-Off Rules

- Home Auto Session is always compiled into receiver runtime.
- Module remains `off` unless user explicitly enables it.
- Both supported install paths ship with `home_auto_session.enabled=false` and
  `mode=off` by default.
- Enablement/configuration source is local receiver config + local portal only.

### 2) Source of Truth for Active Auto Session

Receiver tracks a support-safe active-session authority marker:

- `cloud_acknowledged`: start/stop action acknowledged by cloud API
- `local_recovered_unverified`: active state recovered on restart without cloud
  verify endpoint
- `conflict_unresolved`: cloud/local state disagreement requiring operator
  attention

Rule:

- In uncertainty/conflict, receiver favors safe blocked/degraded behavior over
  speculative control actions.

### 3) Receiver/Cloud Reconciliation Rules

- Startup reconciliation keeps current M2 behavior, with production conflict
  categories.
- Start/stop actions record `last_action`, `last_action_at`, `last_action_result`.
- Conflict classes become explicit and stable:
  - `already_active_conflict`
  - `already_closed_resolved`
  - `state_mismatch_conflict`
  - lifecycle blocks: `revoked`, `disabled`, `replaced`

### 4) Conflict and Lifecycle Handling

Expected handling:

- start rejected as already active -> enter conflict/blocked state (no churn)
- stop rejected as already closed -> resolve as safe stop completion
- revoked/disabled/replaced -> lifecycle-blocked module state; stop further
  control attempts until local intervention/reset/re-pair
- cloud/local mismatch -> stable conflict state + clear operator guidance

### 5) Portal UX and Diagnostics Expectations

Portal must clearly show:

- enabled/mode
- current control state
- active session state and source
- tracked node state summary
- last action taken + result
- last conflict/error and recommended next step

Diagnostics (`doctor`, `status`, `support-snapshot`) must reflect the same
production control vocabulary.

### 6) Linux Existing-OS + Pi Appliance Inclusion

Milestone 3 requires explicit docs confirmation that both supported paths ship:

- same embedded module behavior
- feature included but default-off
- local portal as the normal enable/configure path

## Files and Areas to Change Next

Core runtime/state/status:

- `internal/homeautosession/module.go`
- `internal/homeautosession/module_test.go`
- `internal/state/state.go`
- `internal/state/state_test.go`
- `internal/status/model.go`
- `internal/status/model_test.go`

Portal and diagnostics:

- `internal/webportal/server.go`
- `internal/webportal/templates/home_auto_session.tmpl`
- `internal/webportal/server_test.go`
- `internal/diagnostics/snapshot.go`
- `internal/diagnostics/snapshot_test.go`
- `cmd/loramapr-receiverd/main.go`

Runtime/cloud status integration:

- `internal/runtime/service.go`

Docs/release:

- `docs/home-auto-session.md`
- `docs/local-portal.md`
- `docs/diagnostics.md`
- `docs/runtime-config-state.md`
- `docs/linux-pi-distribution.md`
- `docs/raspberry-pi-appliance.md`
- `docs/reviewer-smoke-test.md`
- `docs/release-notes-v2.11.0.md`
- `docs/release-notes.md`
- `docs/README.md`

## Concise Summary

- M3 productizes control semantics rather than adding policy complexity.
- Receiver becomes explicit about control truth/conflict/lifecycle blocking and
  exposes support-safe action/result visibility consistently.
- Linux existing-OS and Pi appliance paths ship the same embedded optional
  default-off feature with clear user-facing enablement guidance.
