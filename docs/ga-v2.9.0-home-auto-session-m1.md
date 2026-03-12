# v2.9.0 Plan: Embedded Home Auto Session (Milestone 1)

Status: Implemented

Milestone: `v2.9.0`

## Goal

Embed Home Auto Session into `loramapr-receiverd` as an optional runtime module
that observes Meshtastic events and can start/stop one cloud session per receiver
using simple geofence transitions.

Milestone 1 is intentionally narrow:

- one home geofence
- explicit tracked node IDs
- one active auto session per receiver
- local-config-first policy
- observe/control modes

## Current Integration Points (Audit)

- runtime loop and Meshtastic event consumption are centralized in:
  - `internal/runtime/service.go`
- normalized Meshtastic events are available via:
  - `internal/meshtastic/adapter.go`
  - `internal/meshtastic/normalize.go`
- cloud API wiring is isolated in:
  - `internal/cloudclient/client.go`
- persisted receiver state and schema migration are in:
  - `internal/state/state.go`
- runtime status surface and portal feed are in:
  - `internal/status/model.go`
  - `internal/webportal/server.go`
- diagnostics/support snapshot plumbing is in:
  - `internal/diagnostics/*`

These are the correct landing zones for embedded module integration without
introducing a sidecar daemon.

## Module Boundary and Responsibilities

New module package (receiver-embedded):

- `internal/homeautosession`

Responsibilities:

1. consume normalized Meshtastic events asynchronously (bounded queue)
2. track minimal per-node facts needed for decisioning
3. evaluate start/stop decisions from geofence transitions + idle timeout
4. enforce mode behavior:
   - `off`: disabled/no decision execution
   - `observe`: evaluate and report "would start/stop"
   - `control`: execute cloud start/stop requests
5. persist dedupe and active-session state for restart safety
6. expose module status for portal/diagnostics/cloud heartbeat status payload

Non-responsibilities in Milestone 1:

- no advanced policy engine
- no multi-geofence rules
- no cross-receiver coordination
- no separate daemon/service

## Event Subscription Model

Runtime keeps forwarding as primary path:

1. Meshtastic event arrives in `runtime.onMeshtasticEvent`.
2. Existing ingest queue path runs first and remains unchanged/non-blocking.
3. Home Auto Session module receives a copy through a bounded, non-blocking
   enqueue API.
4. If module queue is full, module records a dropped-observation counter/state;
   forwarding is never blocked.

## Decision and State Machine (Milestone 1)

Module status states:

- `disabled`
- `misconfigured`
- `observe_ready`
- `control_ready`
- `start_pending`
- `active`
- `stop_pending`
- `cooldown`
- `degraded`

Core transition logic:

- start candidate: tracked node transitions `inside -> outside` and remains
  outside for `start_debounce`
- stop candidate: tracked node transitions `outside -> inside` and remains
  inside for `stop_debounce`
- idle stop: active session and no qualifying tracked-node updates for
  `idle_stop_timeout`
- control mode executes cloud calls; observe mode only reports intent

Deduplication:

- `last_start_dedupe_key` and `last_stop_dedupe_key` persisted in state
- keys prevent duplicate cloud start/stop on retry/restart

## Config and Persisted State

Config block (local config file):

- `home_auto_session.enabled`
- `home_auto_session.mode` (`off|observe|control`)
- `home_auto_session.home.lat`
- `home_auto_session.home.lon`
- `home_auto_session.home.radius_m`
- `home_auto_session.tracked_node_ids[]`
- `home_auto_session.start_debounce`
- `home_auto_session.stop_debounce`
- `home_auto_session.idle_stop_timeout`
- `home_auto_session.startup_reconcile`
- optional templates:
  - `session_name_template`
  - `session_notes_template`

Persisted state block:

- `active_session_id`
- `active_trigger_node_id`
- `last_decision_reason`
- `last_start_dedupe_key`
- `last_stop_dedupe_key`
- `last_error`
- module runtime status marker/timestamps needed for restart-safe behavior

## Portal and Status Integration

Portal additions:

- Home Auto Session section/page with:
  - enabled/mode
  - current module state
  - geofence summary
  - tracked nodes summary
  - active session summary
  - last decision reason / last error
- simple config form for Milestone 1 fields
- plain-language state guidance (`waiting`, `would start`, `active`, `misconfigured`, etc.)

Status/diagnostics additions:

- status snapshot fields for module state/reason/error/session
- `doctor`, `status`, and `support-snapshot` include module context

## Cloud Session Client Assumptions

Milestone 1 keeps cloud contract behind a thin abstraction:

- `StartSession(...)`
- `StopSession(...)`

Assumed characteristics:

- receiver-authenticated using durable receiver credential path
- explicit idempotency key on start/stop requests
- non-blocking retries handled by module state machine

If exact endpoints differ cloud-side, adapter remains isolated to
`internal/cloudclient`.

## Files/Areas to Change Next

Primary code areas:

- `internal/config/config.go` (+ tests + `receiver.example.json`)
- `internal/state/state.go` (+ tests)
- `internal/status/model.go` (+ tests)
- `internal/runtime/service.go` (+ tests)
- `internal/cloudclient/client.go` (+ tests)
- `internal/homeautosession/*` (new)
- `internal/webportal/server.go` + templates + tests
- `internal/diagnostics/snapshot.go` (+ tests)
- `cmd/loramapr-receiverd/main.go` (doctor/status output)

Primary docs:

- `docs/runtime-config-state.md`
- `docs/local-portal.md`
- `docs/diagnostics.md`
- `docs/reviewer-smoke-test.md`
- `docs/release-notes-v2.9.0.md`
- `docs/README.md` + `docs/release-notes.md`

## Concise Summary

- Current receiver architecture already has clean hooks for an embedded optional
  module.
- Target shape is a bounded async observer + small decision engine, isolated from
  forwarding path.
- Runtime/config/state/portal/diagnostics landing zones are explicit for the
  next prompts.
