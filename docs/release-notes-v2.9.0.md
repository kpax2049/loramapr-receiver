# LoRaMapr Receiver v2.9.0 (Embedded Home Auto Session Milestone 1)

Release date: 2026-03-12

## Highlights

- Added embedded optional `home_auto_session` module inside `loramapr-receiverd`
  (no sidecar daemon).
- Added local config model and validation for Milestone 1:
  - mode `off|observe|control`
  - single home geofence
  - tracked node IDs
  - start/stop debounce and idle stop timeout
  - startup reconcile and session templates
- Added persisted Home Auto Session runtime state:
  - active session/trigger node
  - last decision reason
  - start/stop dedupe keys
  - last error and cooldown markers
- Added asynchronous observation + decision loop:
  - inside->outside start candidate
  - outside->inside stop candidate
  - idle timeout stop candidate
  - observe mode intent reporting
  - control mode cloud session start/stop calls
- Added cloud client wiring for receiver-authenticated session start/stop with
  idempotency headers.
- Added local portal Home Auto Session page:
  - module status visibility
  - local config form
  - safe local actions (`reevaluate`, `reset degraded/cooldown`)
- Added doctor/status/support-snapshot visibility for Home Auto Session state.

## Scope and Safety

- Packet forwarding remains primary and non-blocking.
- Home Auto Session runs behind bounded async observation queue.
- Milestone 1 intentionally limits policy complexity:
  - one home geofence
  - explicit tracked node IDs
  - one active auto session per receiver
