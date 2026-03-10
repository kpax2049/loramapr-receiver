# Runtime Config and State

This document defines the current receiver runtime config/state files for
`loramapr-receiverd`.

## Config File

- Default local path: `./receiver.json`
- Runtime flag: `-config /path/to/receiver.json`
- Expected permissions: owner-readable/writeable (recommended `0600`)

Example shape:

```json
{
  "service": {
    "mode": "auto",
    "heartbeat": "30s"
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
  "meshtastic": {
    "transport": "serial",
    "device": "/dev/ttyUSB0"
  },
  "logging": {
    "level": "info",
    "format": "json"
  }
}
```

### `service.mode`

- `auto`: use persisted pairing phase to select setup/service mode
- `setup`: force first-run/setup runtime behavior
- `service`: force service mode (readiness remains false unless paired steady-state exists)

### `meshtastic.transport`

- `serial`: auto-detect serial device candidates, then consume JSON event stream
- `json_stream`: consume JSON event stream from configured `meshtastic.device`
- `disabled`: adapter inactive

## State File

- Configured by `paths.state_file`
- Default local path: `./data/receiver-state.json`
- Stored with atomic rewrite (temp file + rename)
- File permissions target: `0600`

Persisted fields currently include:

- installation identity (`installation.id`)
- installation timestamps (`created_at`, `last_started_at`)
- pairing/bootstrap phase and retry/error metadata (`pairing.*`)
- cloud endpoint, activation endpoint, and durable receiver credentials (`cloud.*`)
- detected runtime profile + selected mode (`runtime.*`)
- generic metadata timestamp (`metadata.updated_at`)

Important persisted pairing/cloud fields:

- `pairing.pairing_code` (transient until bootstrap exchange)
- `pairing.activation_token` + `pairing.activation_expires_at`
- `pairing.next_retry_at` + `pairing.retry_count`
- `cloud.ingest_api_key_secret` (durable ingest credential)

These values are intentionally **not** exposed by `/api/status`.

## Typical Packaged Paths (Planned Linux-first)

- Config: `/etc/loramapr/receiver.json`
- State: `/var/lib/loramapr/receiver-state.json`
- Service unit: `packaging/systemd/loramapr-receiverd.service`

Packaging work is phased; these paths are the intended service-mode target layout.
