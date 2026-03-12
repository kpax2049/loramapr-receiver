# Embedded Home Auto Session (Milestone 1)

Home Auto Session is an optional embedded receiver module. It is not a separate
daemon and does not block packet forwarding.

## Purpose

When enabled, the module observes normalized Meshtastic packet events and applies
a simple local geofence rule for configured tracked node IDs.

Modes:

- `off`: module disabled
- `observe`: evaluate start/stop decisions but do not call cloud session APIs
- `control`: evaluate decisions and call cloud session start/stop APIs

## Milestone 1 Scope

Milestone 1 intentionally supports only:

- one home geofence
- explicit tracked node IDs
- one active auto session per receiver
- start on `inside -> outside` transition after start debounce
- stop on `outside -> inside` transition after stop debounce
- stop on idle timeout when active

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

## Runtime State

Persisted local state includes:

- `active_session_id`
- `active_trigger_node_id`
- `last_decision_reason`
- `last_start_dedupe_key`
- `last_stop_dedupe_key`
- `last_error`
- `cooldown_until`

This supports restart-safe dedupe and operator diagnostics.

## Cloud Session Contract Assumptions

Milestone 1 uses a thin receiver-authenticated client with configurable endpoint
paths:

- start endpoint (default): `/api/receiver/home-auto-session/start`
- stop endpoint (default): `/api/receiver/home-auto-session/stop`

Requests include idempotency via `x-idempotency-key` and use receiver durable
credential auth (`x-api-key`).

## Portal and Diagnostics

Portal exposes:

- enabled/mode
- current module state
- geofence/tracked-node summary
- active session summary
- last decision reason
- last error

Diagnostics surfaces (`doctor`, `status`, `support-snapshot`) include the same
support-safe module context.

## Troubleshooting Basics

- `misconfigured`: verify geofence and tracked node IDs
- `observe_ready`: module is evaluating but not issuing cloud control calls
- `control_ready`: waiting for transition; no active session
- `cooldown`: recent cloud/session error; module will retry after cooldown
- `degraded`: recover by fixing config/connectivity and using portal reset/reevaluate
