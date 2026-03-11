# v2.3.0 Plan: Receiver Lifecycle Management

Status: Implemented (`v2.3.0`)

Milestone: `v2.3.0`

## Scope

Receiver-side lifecycle behavior only:

- revoked credential handling
- disabled receiver handling
- replaced/superseded receiver handling
- local reset and re-pair behavior
- reinstall/recovery semantics

Out of scope:

- cloud-side policy/UX implementation details
- new install path redesign
- major runtime architecture changes

## Current State Audit

Implemented today:

- pairing/bootstrap/activation state machine (`unpaired` -> `steady_state`)
- durable receiver credential persistence in local state
- steady-state heartbeat + ingest loops
- diagnostics taxonomy and support snapshot
- local portal pairing/progress/troubleshooting
- Linux/Pi existing-OS and Pi appliance install paths

Current lifecycle gaps:

1. Cloud lifecycle events (revoked/disabled/replaced) are not mapped into explicit
   local lifecycle states.
2. Runtime may continue trying steady-state cloud traffic after credential
   lifecycle changes, instead of moving into an explicit blocked/re-pair state.
3. Local reset/deauthorization flow is not explicit as a first-class operation.
4. Docs do not yet define reinstall/replacement identity semantics clearly for
   lifecycle recovery.

## v2.3.0 Target Behavior

### Lifecycle Events and Local State

When cloud responses indicate lifecycle invalidation:

- `credential_revoked`
- `receiver_disabled`
- `receiver_replaced`

receiver must:

1. transition to a clear local state (`pairing.phase=unpaired` with explicit
   lifecycle `pairing.last_change`)
2. stop steady-state forwarding/active heartbeat behavior
3. persist lifecycle reason across restart
4. expose actionable guidance in portal/diagnostics
5. provide a clear local re-pair/reset operation

### Reset / Re-pair Semantics

Two local operator intents are required:

1. `reset pairing`:
   - clears transient pairing flow state
   - preserves install identity and config
   - can be used to restart onboarding flow
2. `deauthorize + re-pair`:
   - clears durable receiver credentials locally
   - returns receiver to unpaired setup state
   - requires cloud pairing flow again

### Reinstall and Recovery Semantics

Policy target for supported install paths:

- reinstall/upgrade over existing state path:
  - preserve `installation.id`
  - preserve local state unless explicit purge/reset action
- remove (non-purge):
  - retain config/state by package policy
- purge / fresh image / new storage:
  - new install identity generated
  - pairing required again

## Portal and Diagnostics Expectations

Portal must present lifecycle failures as human-readable states with specific
guidance:

- revoked: re-pair to issue fresh receiver credentials
- disabled: receiver blocked by cloud-side policy; re-enable/re-pair guidance
- replaced: this instance is superseded; reset/re-pair as replacement flow

Diagnostics/support snapshot must expose coarse lifecycle status without
credential leakage.

## Implementation Landing Zones

Primary code/files to change in next prompts:

- runtime lifecycle handling:
  - `internal/runtime/service.go`
  - `internal/runtime/service_test.go`
- pairing/lifecycle state transitions and local reset flow:
  - `internal/pairing/manager.go`
  - `internal/pairing/manager_test.go`
- failure taxonomy updates:
  - `internal/diagnostics/taxonomy.go`
  - `internal/diagnostics/taxonomy_test.go`
- portal lifecycle UX updates:
  - `internal/webportal/server.go`
  - `internal/webportal/templates/*.tmpl`
  - `internal/webportal/server_test.go`
- CLI lifecycle/reset operations:
  - `cmd/loramapr-receiverd/main.go`
- state/config docs and lifecycle docs:
  - `docs/pairing-lifecycle.md`
  - `docs/runtime-config-state.md`
  - `docs/diagnostics.md`
  - `docs/local-portal.md`
  - `docs/linux-package-lifecycle.md`
  - `docs/raspberry-pi-appliance.md`

## Concise Summary

- Current path: pairing and forwarding work, but lifecycle invalidation handling
  is coarse and not yet explicit.
- Gap to close: convert revoked/disabled/replaced cloud responses into stable,
  restart-safe local lifecycle states with clear recovery actions.
- v2.3.0 output: receiver that fails safely, stops active forwarding when
  invalidated, and guides the user to reset/re-pair.
- Next changes: runtime + pairing transition logic, diagnostics/portal messaging,
  local reset command flow, and lifecycle/reinstall docs/tests.
