# Embedded Home Auto Session (Milestone 3: Production Control Model)

Home Auto Session is an optional embedded module inside `loramapr-receiverd`.
It is never a separate service and never blocks packet forwarding.

## Default-Off and Enablement

- Module is included in all supported Linux/Pi receiver builds.
- Default is always off:
  - `home_auto_session.enabled = false`
  - `home_auto_session.mode = off`
- User must explicitly enable it in local config or local portal.

Modes:

- `off`: module disabled
- `observe`: evaluate decisions but never call session start/stop APIs
- `control`: evaluate decisions and call session start/stop APIs

## Policy Scope (Intentionally Narrow)

- one home geofence
- explicit tracked node IDs
- one active auto session per receiver
- start on `inside -> outside` transition after debounce
- stop on `outside -> inside` transition or idle timeout

Milestone 3 does not add advanced policy features.

## Production Control States

Module state:

- `disabled`
- `misconfigured`
- `observe_ready`
- `control_ready`
- `start_pending`
- `active`
- `stop_pending`
- `cooldown`
- `degraded`

Control state (operator-facing control semantics):

- `disabled`
- `misconfigured`
- `ready`
- `pending_start`
- `pending_stop`
- `active`
- `cooldown`
- `conflict_blocked`
- `lifecycle_blocked`
- `degraded`

Active state source:

- `none`
- `cloud_acknowledged`
- `local_recovered_unverified`
- `conflict_unresolved`

## Reconciliation and Conflict Handling

Startup reconciliation outcomes:

- `clean_idle`
- `startup_reconcile_disabled`
- `active_recovered_unverified`
- `pending_start_recovering`
- `pending_stop_recovering`
- `pending_start_resolved`
- `pending_stop_resolved`
- `inconsistent_degraded`

Production conflict/lifecycle outcomes:

- `conflict_already_active`
- `conflict_state_mismatch`
- `lifecycle_revoked`
- `lifecycle_disabled`
- `lifecycle_replaced`

Behavior:

- already-active start conflicts block churn and move to conflict-blocked state
- already-closed stop conflicts resolve as safe stop completion
- revoked/disabled/replaced lifecycle responses block control actions
- unresolved cloud/local mismatch remains explicit until operator intervention

## Action and Diagnostics Fields

Support-safe action metadata:

- `last_action`
- `last_action_result`
- `last_action_at`
- `last_successful_action`
- `last_successful_action_at`

Other support fields:

- `tracked_node_state`
- `reconciliation_state`
- `active_state_source`
- `blocked_reason`
- `last_error`
- `gps_status` and `gps_reason`

These are surfaced consistently in portal, `doctor`, `status`, and
`support-snapshot`.

## Configuration

`home_auto_session` config keys:

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

## Supported Install Paths

Same embedded module behavior on both supported paths:

- [Linux/Pi Existing-OS Install Path](./linux-pi-distribution.md)
- [Raspberry Pi Appliance Path](./raspberry-pi-appliance.md)

Feature remains optional/off-by-default in both paths.

## Troubleshooting Basics

- `misconfigured`: fix geofence/tracked-node config
- `cooldown`: retry window after retryable cloud/session API failure
- `conflict_blocked`: cloud/local state disagreement requires intervention
- `lifecycle_blocked`: receiver revoked/disabled/replaced; reset/re-pair required
- `degraded`: non-retryable control path issue; inspect `blocked_reason`
- GPS `missing|invalid|stale|boundary_uncertain`: no control action until usable position data
