# Receiver Diagnostics, Attention, and Ops Taxonomy

This document defines receiver-side diagnostics behavior for v2.6.0 operational
automation and notifications alignment.

## Commands and Surfaces

- `doctor`: local diagnostics with operational checks (`-json` supported)
- `status`: structured local status output
- `support-snapshot`: redacted support bundle export (JSON)
- portal/API:
  - `GET /api/status`
  - `GET /api/ops`

## Coarse Failure Taxonomy

Stable receiver-side failure codes:

- `pairing_code_invalid`
- `pairing_code_expired`
- `activation_failed`
- `pairing_not_completed`
- `receiver_credential_revoked`
- `receiver_disabled`
- `receiver_replaced`
- `receiver_auth_invalid`
- `receiver_outdated`
- `receiver_version_unsupported`
- `cloud_config_incompatible`
- `local_schema_incompatible`
- `cloud_unreachable`
- `network_unavailable`
- `portal_unavailable`
- `no_serial_device_detected`
- `node_detected_not_connected`
- `events_not_forwarding`

## Attention Model

Attention is derived from failure taxonomy plus operational checks and is
exposed consistently through portal, CLI, support bundle, and cloud heartbeat
status payloads.

Attention states:

- `none`
- `info`
- `action_required`
- `urgent`

Attention categories:

- `pairing`
- `connectivity`
- `authorization`
- `lifecycle`
- `node`
- `forwarding`
- `version`
- `compatibility`
- `service`

Representative mappings:

- `pairing_not_completed`, `pairing_code_invalid`, `pairing_code_expired`, `activation_failed` -> `pairing` / `action_required`
- `cloud_unreachable`, `network_unavailable`, `portal_unavailable` -> `connectivity` / `action_required`
- `receiver_auth_invalid` -> `authorization` / `urgent`
- `receiver_credential_revoked`, `receiver_disabled`, `receiver_replaced` -> `lifecycle` / `urgent`
- `no_serial_device_detected`, `node_detected_not_connected` -> `node` / `action_required`
- `events_not_forwarding` -> `forwarding` / `action_required`
- `receiver_outdated` -> `version` / `action_required`
- `receiver_version_unsupported` -> `version` / `urgent`
- `local_schema_incompatible`, `cloud_config_incompatible` -> `compatibility` / `urgent`

## Operational Checks Model

Support-focused checks are evaluated as:

- `service_health`
- `pairing_authorized`
- `cloud_reachability`
- `node_connection`
- `forwarding_activity`
- `update_supportability`

Each check emits level:

- `ok`
- `warn`
- `fail`
- `unknown`

Overall state:

- `ok`
- `degraded`
- `blocked`

## Support Snapshot Export

Generate:

```bash
loramapr-receiverd support-snapshot -config /etc/loramapr/receiver.json -out /tmp/receiver-support.json
```

Bundle includes:

- runtime metadata: version/channel/build/platform/install type
- config/state markers: config schema, state schema, runtime profile, paths
- pairing/lifecycle state and authorization summary
- cloud probe + cloud config version marker
- network probe + local runtime status probe
- meshtastic detection/connection summary
- update status and recommendation summary
- diagnostics failure code/summary/hint
- attention state/category/code/summary/hint
- operational checks and overall state
- recent coarse errors

If config/state loading is incompatible, export still writes a compatibility
snapshot with `local_schema_incompatible` diagnostics to aid support.

## Redaction Guarantees

Support snapshot never exports raw secret values:

- `cloud.ingest_api_key_secret`
- `cloud.credential_ref`
- `pairing.pairing_code`
- `pairing.activation_token`

Only support-safe booleans/metadata are included.

## Field Triage Workflow

1. Run `doctor` (human or JSON) and capture failure code + operational checks.
2. Capture attention fields (`attention_state`, `attention_code`, `attention_hint`).
3. Export `support-snapshot`.
4. Compare portal Troubleshooting and `/api/ops` with CLI output.
5. For lifecycle invalidation (`revoked`, `disabled`, `replaced`), reset/re-pair.
6. For supportability failures (`receiver_version_unsupported`), upgrade receiver.

Detailed scenario runbook is in:

- `docs/support-operations-workflow.md`
