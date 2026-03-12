# v2.10.0 Plan: Embedded Home Auto Session Milestone 2 (Correctness and Recovery)

Status: Implemented

Milestone: `v2.10.0`

## Goal

Harden Home Auto Session behavior under restart, retry, flaky GPS, geofence
jitter, and cloud/API failures without expanding policy scope.

Milestone 2 does not add new policy features. It makes Milestone 1 safe and
predictable in failure/recovery paths.

## Scope Constraints

Preserved model:

- one home geofence
- explicit tracked node IDs
- one active auto session per receiver
- packet forwarding remains primary and non-blocking

Out of scope:

- multi-geofence or advanced policy model
- cross-receiver coordination
- sidecar/service split

## Current Reliability Gaps (Before M2)

1. Startup recovery can restore local state, but pending start/stop calls are not
   explicitly reconciled.
2. Dedupe keys exist, but pending action persistence across restart is incomplete.
3. GPS handling treats missing/invalid/jittered position data too coarsely.
4. Boundary jitter can cause noisy transition candidates.
5. Retryable and non-retryable cloud errors need clearer blocked/degraded behavior.
6. Stop-path retries need stronger “no duplicate closure” guarantees.

## Target Correctness and Recovery Behavior

### 1) Startup Reconciliation

On startup, module explicitly classifies persisted state:

- `clean_idle`: no active/pending session state
- `active_recovered_unverified`: local active session recovered (no cloud verify endpoint available)
- `pending_start_recovering` / `pending_stop_recovering`: retry same pending action with same dedupe key
- `inconsistent_degraded`: local state contradiction requires operator intervention

Cloud verification:

- If no dedicated verify endpoint exists, module remains conservative and uses
  local state + idempotent action retry only.

### 2) Idempotency and Dedupe

- Persist both successful and pending action metadata:
  - pending action kind, trigger node, reason, dedupe key, pending timestamp
  - last successful action, timestamp
  - last successful start/stop dedupe keys
- Start/stop retries reuse the same dedupe key after restart.
- Duplicate start/stop execution is suppressed via persisted dedupe markers.

### 3) Geofence Flap/Jitter Protection

- Add explicit boundary uncertainty band around radius.
- Inside/outside transition only accepted when point is outside uncertainty band.
- Maintain debounce behavior plus post-action stabilization cooldown to prevent
  immediate flip-flop after action execution.

### 4) GPS Validity Handling

Explicit GPS status states:

- `missing`
- `invalid`
- `stale`
- `boundary_uncertain`
- `valid`

Rules:

- no auto start from missing/invalid/stale/uncertain position
- module surfaces waiting/degraded reason (not silent no-op)
- idle stop still available when appropriate

### 5) Degraded/Error State Model

Clear degraded semantics:

- retryable cloud failures -> cooldown with bounded retry backoff
- non-retryable/auth/policy failures -> degraded + blocked reason, no churn
- local inconsistent state -> degraded until manual reset/reevaluate

### 6) Stop-Path Correctness

Stop triggers remain:

- outside->inside transition (with debounce)
- idle timeout

Correctness requirements:

- one active session stop intent at a time
- retries reuse same stop dedupe key
- if restart shows no active session for pending stop, resolve as completed

### 7) Offline/Cloud-Unreachable Recovery

- cooldown-based retry for retryable failures
- bounded backoff to avoid API spam
- preserve pending action for safe retry on reevaluate/tick
- expose last error, blocked reason, and next-step guidance in portal/diagnostics

## Integration Points to Change

Core module and state:

- `internal/homeautosession/module.go`
- `internal/homeautosession/module_test.go`
- `internal/state/state.go`
- `internal/state/state_test.go`
- `internal/status/model.go`
- `internal/status/model_test.go`

Event and cloud surfaces:

- `internal/meshtastic/normalize.go` (+ tests) for position validity hints
- `internal/meshtastic/adapter.go`
- `internal/cloudclient/client.go` (+ tests) for retry/degraded classification use
- `internal/runtime/service.go` (+ tests) status/heartbeat propagation

Operator visibility:

- `internal/webportal/server.go`
- `internal/webportal/templates/home_auto_session.tmpl`
- `internal/webportal/server_test.go`
- `internal/diagnostics/snapshot.go` (+ tests)
- `cmd/loramapr-receiverd/main.go`

Docs:

- `docs/home-auto-session.md`
- `docs/runtime-config-state.md`
- `docs/local-portal.md`
- `docs/diagnostics.md`
- `docs/support-operations-workflow.md`
- `docs/reviewer-smoke-test.md`
- `docs/release-notes-v2.10.0.md`

## Concise Summary

- M2 shifts Home Auto Session from “works in normal path” to “safe under restart,
  jitter, and failures.”
- Recovery is explicit via persisted pending/success action markers and startup
  reconciliation outcomes.
- GPS quality and cloud/API failure paths now produce stable, support-readable
  module behavior instead of action churn.
