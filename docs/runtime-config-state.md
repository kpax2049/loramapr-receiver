# Runtime Config and State

This document defines the receiver runtime config/state layout and migration
behavior for `loramapr-receiverd`.

## Config File

- Default local path: `./receiver.json`
- Runtime flag: `-config /path/to/receiver.json`
- Recommended permissions: `0600`

Current config schema:

- `schema_version: 3`

Key sections:

- `service`
- `runtime`
- `paths`
- `portal`
- `cloud`
- `update`
- `meshtastic`
- `home_auto_session`
- `logging`

Example (local-dev defaults):

```json
{
  "schema_version": 3,
  "service": {
    "mode": "auto",
    "heartbeat": "30s"
  },
  "runtime": {
    "profile": "auto",
    "local_name": ""
  },
  "paths": {
    "state_file": "./data/receiver-state.json"
  },
  "portal": {
    "bind_address": "127.0.0.1:8080"
  },
  "cloud": {
    "base_url": "https://api.loramapr.example"
  },
  "update": {
    "enabled": false,
    "manifest_url": "",
    "check_interval": "6h",
    "request_timeout": "4s",
    "min_supported_version": ""
  },
  "meshtastic": {
    "transport": "serial",
    "bootstrap_write": false,
    "bridge_command": "",
    "bridge_args": []
  },
  "home_auto_session": {
    "enabled": false,
    "mode": "off",
    "home": {
      "lat": 0,
      "lon": 0,
      "radius_m": 150
    },
    "tracked_node_ids": [],
    "start_debounce": "30s",
    "stop_debounce": "30s",
    "idle_stop_timeout": "15m",
    "startup_reconcile": true,
    "session_name_template": "Home Auto {{.NodeID}}",
    "session_notes_template": "Automatically managed by LoRaMapr Receiver",
    "cloud": {
      "start_endpoint": "/api/receiver/home-auto-session/start",
      "stop_endpoint": "/api/receiver/home-auto-session/stop"
    }
  },
  "logging": {
    "level": "info",
    "format": "json"
  }
}
```

Linux/Pi package installs do not use the local-dev defaults above.

Packaged Linux/Pi defaults (`/etc/loramapr/receiver.json`) are:

- `runtime.profile = "linux-service"`
- `paths.state_file = "/var/lib/loramapr/receiver-state.json"`
- `portal.bind_address = "0.0.0.0:8080"`
- `cloud.base_url = "https://loramapr.com"`
- `meshtastic.transport = "bridge"` (auto-detect device if not pinned)
- `meshtastic.bootstrap_write = false` (serial-mode startup writes disabled)

Packaged config source template:

- `packaging/linux/receiver.linux-package.json`

Cloud endpoint can be changed safely without manual JSON edits:

```bash
sudo /usr/bin/loramapr-receiverd configure-cloud -config /etc/loramapr/receiver.json -base-url https://loramapr.com
sudo systemctl restart loramapr-receiverd
```

### `service.mode`

- `auto`: setup unless pairing phase is `steady_state`.
- `setup`: force setup-first behavior.
- `service`: force service mode (readiness still depends on compatible pairing/cloud config).

### `runtime.profile`

- `auto`
- `local-dev`
- `linux-service`
- `windows-user`
- `appliance-pi` (deprecated profile retained for compatibility)

### `runtime.local_name`

- optional local operator-facing receiver name hint
- used for portal/diagnostics/cloud status context
- if empty, runtime derives a stable default from hostname/install type

### `update` block

- `enabled`: enables manifest-based currentness checks.
- `manifest_url`: URL to cloud manifest fragment.
- `check_interval`: interval between checks.
- `request_timeout`: HTTP timeout for manifest fetch.
- `min_supported_version`: optional support floor for `unsupported` status.

This logic is informational only; no self-update actions are performed.

### `meshtastic.transport`

- `serial`
- `bridge`
- `json_stream`
- `disabled`

Transport behavior:

- `bridge` (packaged default): supervised subprocess bridge feeding
  newline-delimited JSON events into the receiver runtime.
- `serial`: direct native Meshtastic USB serial framing/protobuf path.
- `json_stream`: newline-delimited JSON compatibility mode for fixtures/tests or
  explicit sidecar feeds.
- `disabled`: Meshtastic adapter remains inactive.

### `meshtastic.bridge_command` and `meshtastic.bridge_args`

Optional override for bridge process command/args:

- `bridge_command`: executable path
- `bridge_args`: argument list

Supported token substitution in command and args:

- `{{device}}`
- `${MESHTASTIC_PORT}`
- `$MESHTASTIC_PORT`

### `meshtastic.bootstrap_write`

- `false` (default): passive serial mode; receiver does not write bootstrap
  frames to the attached node on startup.
- `true`: receiver may send a throttled startup bootstrap request to encourage
  native config/status streaming on compatible nodes.

### `home_auto_session` block

Optional embedded Home Auto Session module config:

- `enabled`
- `mode` (`off|observe|control`)
- `home.lat`, `home.lon`, `home.radius_m`
- `tracked_node_ids[]`
- `start_debounce`
- `stop_debounce`
- `idle_stop_timeout`
- `startup_reconcile`
- `session_name_template`
- `session_notes_template`
- `cloud.start_endpoint`, `cloud.stop_endpoint`

Milestone 1 behavior assumptions:

- one home geofence
- explicit tracked node IDs
- one active auto session per receiver

## State File

- Path: `paths.state_file`
- Default local path: `./data/receiver-state.json`
- Atomic persistence: temp file + rename
- Target permissions: `0600`

Current state schema:

- `schema_version: 7`

Top-level sections:

- `installation`
- `pairing`
- `cloud`
- `runtime`
- `update`
- `home_auto_session`
- `metadata`

### Persisted identity and pairing

- `installation.id` (stable local installation identity)
- `installation.local_name` (stable local name hint)
- `installation.hostname` (runtime hostname hint)
- `pairing.phase` (`unpaired`, `pairing_code_entered`, `bootstrap_exchanged`, `activated`, `steady_state`)
- pairing lifecycle metadata (`retry_count`, `next_retry_at`, `last_error`, `last_change`)

### Persisted cloud material

- `cloud.endpoint_url`
- cloud identity labels (`receiver_id`, `receiver_label`) plus optional cloud labels (`site_label`, `group_label`)
- `cloud.config_version`
- `cloud.activate_endpoint`, `cloud.heartbeat_endpoint`, `cloud.ingest_endpoint`
- durable credential fields (`ingest_api_key_id`, `ingest_api_key_secret`, `credential_ref`)

`site_label` and `group_label` are optional metadata and are not required for
pairing, activation, heartbeat, or forwarding.

### Persisted runtime classification

- `runtime.profile`
- `runtime.mode`
- `runtime.install_type`

### Persisted update reasoning

- `update.status`
- `update.summary`
- `update.hint`
- `update.manifest_version`
- `update.manifest_channel`
- `update.recommended_version`
- `update.last_checked_at`
- `update.last_error`

### Persisted Home Auto Session runtime state

- `home_auto_session.module_state`
- `home_auto_session.control_state`
- `home_auto_session.active_state_source`
- `home_auto_session.reconciliation_state`
- `home_auto_session.effective_config_source`
- `home_auto_session.effective_config_version`
- `home_auto_session.cloud_config_present`
- `home_auto_session.last_fetched_config_version`
- `home_auto_session.last_applied_config_version`
- `home_auto_session.last_config_apply_result`
- `home_auto_session.last_config_apply_error`
- `home_auto_session.desired_config_enabled`
- `home_auto_session.desired_config_mode`
- `home_auto_session.active_session_id`
- `home_auto_session.active_trigger_node_id`
- `home_auto_session.pending_action`
- `home_auto_session.pending_trigger_node_id`
- `home_auto_session.pending_reason`
- `home_auto_session.pending_dedupe_key`
- `home_auto_session.pending_since`
- `home_auto_session.last_decision_reason`
- `home_auto_session.last_start_dedupe_key`
- `home_auto_session.last_stop_dedupe_key`
- `home_auto_session.last_action`
- `home_auto_session.last_action_result`
- `home_auto_session.last_action_at`
- `home_auto_session.last_successful_action`
- `home_auto_session.last_successful_action_at`
- `home_auto_session.last_error`
- `home_auto_session.blocked_reason`
- `home_auto_session.consecutive_failures`
- `home_auto_session.last_event_at`
- `home_auto_session.cooldown_until`
- `home_auto_session.decision_cooldown_until`
- `home_auto_session.gps_status`
- `home_auto_session.gps_reason`
- `home_auto_session.gps_node_id`
- `home_auto_session.gps_updated_at`
- `home_auto_session.gps_distance_m`
- `home_auto_session.observed_dropped`

## Migration and Compatibility

### Config

- Legacy/unspecified config schema is migrated to `3`.
- Schema `2 -> 3` migration adds Home Auto Session config block support.
- Config schema newer than supported runtime fails startup.

### State

- Legacy pairing phase values are normalized during migration.
- Schema `2 -> 3` migration adds install type/update defaults.
- Schema `3 -> 4` migration adds persisted Home Auto Session runtime state.
- Schema `4 -> 5` migration adds Home Auto Session reconciliation defaults.
- Schema `5 -> 6` migration adds Home Auto Session control/source defaults.
- Schema `6 -> 7` migration adds Home Auto Session cloud-managed config source,
  version, and apply tracking markers.
- State schema newer than supported runtime fails startup.

### Cloud config compatibility

- Cloud-reported config version is persisted in state.
- Current runtime accepts cloud config major `1`.
- Unsupported cloud config version blocks forwarding/steady-state and surfaces
  explicit diagnostics.

## Reset and Persistence Semantics

Local reset command:

```bash
loramapr-receiverd reset-pairing -config /etc/loramapr/receiver.json
```

Default reset behavior:

- preserves `installation.id`
- clears durable cloud credential material
- returns pairing phase to `unpaired`

Fresh state file (new disk/new SD card/new path) generates a new
`installation.id` and requires fresh pairing.

## Typical Packaged Paths

Linux package/service path:

- config: `/etc/loramapr/receiver.json`
- state: `/var/lib/loramapr/receiver-state.json`
- logs: `/var/log/loramapr/`
- service: `loramapr-receiverd.service`

Historical Pi appliance installs use the same runtime/state model with
`appliance-pi` defaults.
