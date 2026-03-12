# Embedded Home Auto Session (Milestone 4: Cloud-Managed Config)

Home Auto Session is an optional embedded module inside `loramapr-receiverd`.
It is never a separate daemon and never blocks packet forwarding.

## Default-Off and Enablement

- Module is included in all supported Linux/Pi receiver builds.
- Default remains off:
  - `home_auto_session.enabled = false`
  - `home_auto_session.mode = off`
- User must explicitly opt in.

Modes:

- `off`
- `observe`
- `control`

## Policy Scope (Intentionally Narrow)

- one home geofence
- explicit tracked node IDs
- one active auto session per receiver
- start on `inside -> outside` transition after debounce
- stop on `outside -> inside` transition or idle timeout

Milestone 4 adds cloud-managed config application, not policy expansion.

## Cloud-Managed Config Model

Receiver can consume optional cloud-managed Home Auto Session policy from the
receiver-authenticated heartbeat contract.

Precedence is deterministic:

1. valid cloud-managed config (if present)
2. otherwise local fallback config

No deep merge is used in M4. One effective config is active at a time.

### Effective Config Tracking

Receiver persists and exposes:

- `effective_config_source` (`cloud_managed` or `local_fallback`)
- `effective_config_version`
- `cloud_config_present`
- `last_fetched_config_version`
- `last_applied_config_version`
- `last_config_apply_result`
- `last_config_apply_error`
- `desired_config_enabled`
- `desired_config_mode`

These fields are visible in portal, `status`, `doctor`, and `support-snapshot`.

### Safe Fallback Behavior

- no cloud config present: apply local fallback
- valid cloud config present: apply cloud config
- cloud config disables module: effective config disables module
- invalid cloud config: reject and keep/apply local fallback
- cloud unavailable: keep last effective config and report fetch/apply failure

## Runtime and Control States

Module states:

- `disabled`
- `misconfigured`
- `observe_ready`
- `control_ready`
- `start_pending`
- `active`
- `stop_pending`
- `cooldown`
- `degraded`

Control states:

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

## Portal and Diagnostics

Portal route:

- `GET /home-auto-session`

Portal now shows:

- effective config source/version
- cloud-config present state
- last config fetch/apply result
- last config apply error
- desired config vs runtime blocked/degraded conditions

Diagnostics (`doctor`, `status`, `support-snapshot`) expose the same
support-safe config-source/version/apply model.

## Supported Install Paths

Same embedded behavior on both supported paths:

- [Linux/Pi Existing-OS Install Path](./linux-pi-distribution.md)
- [Raspberry Pi Appliance Path](./raspberry-pi-appliance.md)

## Milestone 4 Limitations

- no advanced cloud policy builder
- no remote execution
- no autonomous remediation agent
- local portal remains visibility + fallback config surface
