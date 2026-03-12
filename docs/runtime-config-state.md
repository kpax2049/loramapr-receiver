# Runtime Config and State

This document defines the receiver runtime config/state layout and migration
behavior for `loramapr-receiverd`.

## Config File

- Default local path: `./receiver.json`
- Runtime flag: `-config /path/to/receiver.json`
- Recommended permissions: `0600`

Current config schema:

- `schema_version: 2`

Key sections:

- `service`
- `runtime`
- `paths`
- `portal`
- `cloud`
- `update`
- `meshtastic`
- `logging`

Example (minimal):

```json
{
  "schema_version": 2,
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
    "transport": "serial"
  },
  "logging": {
    "level": "info",
    "format": "json"
  }
}
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
- `appliance-pi`

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
- `json_stream`
- `disabled`

## State File

- Path: `paths.state_file`
- Default local path: `./data/receiver-state.json`
- Atomic persistence: temp file + rename
- Target permissions: `0600`

Current state schema:

- `schema_version: 3`

Top-level sections:

- `installation`
- `pairing`
- `cloud`
- `runtime`
- `update`
- `metadata`

### Persisted identity and pairing

- `installation.id` (stable local installation identity)
- `installation.local_name` (stable local name hint)
- `installation.hostname` (runtime hostname hint)
- `pairing.phase` (`unpaired`, `pairing_code_entered`, `bootstrap_exchanged`, `activated`, `steady_state`)
- pairing lifecycle metadata (`retry_count`, `next_retry_at`, `last_error`, `last_change`)

### Persisted cloud material

- `cloud.endpoint_url`
- cloud identity labels (`receiver_id`, `receiver_label`, `site_label`, `group_label`)
- `cloud.config_version`
- `cloud.activate_endpoint`, `cloud.heartbeat_endpoint`, `cloud.ingest_endpoint`
- durable credential fields (`ingest_api_key_id`, `ingest_api_key_secret`, `credential_ref`)

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

## Migration and Compatibility

### Config

- Legacy/unspecified config schema is migrated to `2`.
- Config schema newer than supported runtime fails startup.

### State

- Legacy pairing phase values are normalized during migration.
- Schema `2 -> 3` migration adds install type/update defaults.
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

Pi appliance path uses the same runtime/state model with appliance profile
defaults.
