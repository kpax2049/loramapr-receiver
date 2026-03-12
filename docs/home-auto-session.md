# Embedded Home Auto Session (Milestone 2: Correctness and Recovery)

Home Auto Session is an optional embedded receiver module. It is not a separate
service and never blocks packet forwarding.

## Purpose

When enabled, the module observes normalized Meshtastic packet events and
applies a simple local geofence policy for tracked node IDs.

Modes:

- `off`: module disabled
- `observe`: evaluate start/stop decisions but do not call cloud session APIs
- `control`: evaluate decisions and call cloud session start/stop APIs

## Scope (Still Intentionally Narrow)

- one home geofence
- explicit tracked node IDs
- one active auto session per receiver
- start on `inside -> outside` transition after start debounce
- stop on `outside -> inside` transition or idle timeout

## Milestone 2 Hardening

Milestone 2 adds correctness/recovery behavior without expanding policy scope.

### Startup Reconciliation

On runtime start, module reconciles persisted state into explicit outcomes:

- `clean_idle`
- `startup_reconcile_disabled`
- `active_recovered_unverified`
- `pending_start_recovering`
- `pending_stop_recovering`
- `pending_start_resolved`
- `pending_stop_resolved`
- `inconsistent_degraded`

### Idempotency and Pending Action Recovery

Persisted control metadata now includes pending action context so retries across
restart use the same dedupe key:

- `pending_action`
- `pending_trigger_node_id`
- `pending_reason`
- `pending_dedupe_key`
- `pending_since`

Successful control metadata:

- `last_start_dedupe_key`
- `last_stop_dedupe_key`
- `last_successful_action`
- `last_successful_action_at`

### GPS Validity and Geofence Flap Protection

Module now classifies tracked-node position quality:

- `missing`
- `invalid`
- `stale`
- `boundary_uncertain`
- `valid`

Safety rules:

- no auto-start from missing/invalid/stale/boundary-uncertain position
- boundary uncertainty band suppresses start/stop flap near geofence radius
- post-action decision cooldown suppresses immediate flip-flop actions

### Failure and Degraded Handling

- retryable cloud/session failures -> `cooldown` with bounded retry backoff
- non-retryable failures -> `degraded` with explicit `blocked_reason`
- persistent failure counters and last error are persisted for support

## Configuration

Use `home_auto_session` in receiver config:

- `enabled`
- `mode`
- `home.lat`, `home.lon`, `home.radius_m`
- `tracked_node_ids`
- `start_debounce`
- `stop_debounce`
- `idle_stop_timeout`
- `startup_reconcile`
- `session_name_template`, `session_notes_template`
- optional cloud endpoint overrides:
  - `cloud.start_endpoint`
  - `cloud.stop_endpoint`

Portal path:

- `GET /home-auto-session`

## Operator Visibility

Portal, `doctor`, `status`, and `support-snapshot` expose support-safe fields:

- module state + reconciliation state
- pending action + active session context
- last decision + last error + blocked reason
- GPS status and reason
- last successful control action

## Cloud Contract Assumptions

Receiver uses receiver-authenticated endpoints (with idempotency keys):

- start endpoint (default): `/api/receiver/home-auto-session/start`
- stop endpoint (default): `/api/receiver/home-auto-session/stop`

No full cloud-side verification endpoint is required for M2.

## Troubleshooting Basics

- `misconfigured`: fix geofence and tracked node IDs
- `cooldown`: transient cloud/API retry window in progress
- `degraded`: non-retryable failure or inconsistent local state; use reset/reevaluate
- `boundary_uncertain`: wait for a stable point outside uncertainty band
- `stale`/`missing`: wait for fresh tracked-node GPS position updates
