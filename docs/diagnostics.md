# Receiver Diagnostics and Attention States

This document explains the local diagnostics surfaces and the stable
failure/attention language used across portal, CLI, and cloud status payloads.

## Quick Diagnostics Commands

```bash
loramapr-receiverd doctor -config /etc/loramapr/receiver.json
loramapr-receiverd status -config /etc/loramapr/receiver.json
loramapr-receiverd support-snapshot -config /etc/loramapr/receiver.json -out /tmp/receiver-support.json
```

## Available Surfaces

- CLI:
  - `doctor` (human-readable and `-json`)
  - `status` (structured JSON status)
  - `support-snapshot` (redacted export for support)
- Portal APIs:
  - `GET /api/status`
  - `GET /api/ops`

## Stable Failure Codes

Receiver uses stable coarse failure codes:

- pairing/setup:
  - `pairing_code_invalid`
  - `pairing_code_expired`
  - `activation_failed`
  - `pairing_not_completed`
- lifecycle/auth:
  - `receiver_credential_revoked`
  - `receiver_disabled`
  - `receiver_replaced`
  - `receiver_auth_invalid`
- connectivity/runtime:
  - `cloud_unreachable`
  - `network_unavailable`
  - `portal_unavailable`
  - `cloud_config_incompatible`
  - `local_schema_incompatible`
- node/forwarding:
  - `no_serial_device_detected`
  - `node_detected_not_connected`
  - `events_not_forwarding`
- supportability:
  - `receiver_outdated`
  - `receiver_version_unsupported`

## Attention Model

Attention is derived from failure code plus operational checks.

States:

- `none`
- `info`
- `action_required`
- `urgent`

Categories:

- `pairing`, `connectivity`, `authorization`, `lifecycle`, `node`,
  `forwarding`, `version`, `compatibility`, `service`

Typical mappings:

- `pairing_not_completed` -> `action_required` / `pairing`
- `cloud_unreachable` -> `action_required` / `connectivity`
- `receiver_credential_revoked` -> `urgent` / `lifecycle`
- `no_serial_device_detected` -> `action_required` / `node`
- `receiver_version_unsupported` -> `urgent` / `version`

## Operational Checks

`/api/ops` and `doctor` include checks:

- `service_health`
- `pairing_authorized`
- `cloud_reachability`
- `node_connection`
- `forwarding_activity`
- `update_supportability`

Each check level:

- `ok`
- `warn`
- `fail`
- `unknown`

Overall state:

- `ok`
- `degraded`
- `blocked`

## Support Snapshot Export

`support-snapshot` includes support-safe context:

- runtime version/channel/build/platform/install type
- identity context:
  - installation id
  - local name and hostname hints
  - cloud receiver/site/group labels (when present)
- Home Auto Session context:
  - enabled/mode
  - effective config source/version
  - cloud-config present
  - last fetched/applied config versions
  - last config apply result/error
  - desired config enabled/mode
  - module + control + reconciliation state
  - active state source
  - pending action state (if recovering/retrying)
  - tracked nodes summary
  - active session/trigger node
  - last action + last action result
  - last decision reason
  - last successful action
  - GPS validity status/reason
  - blocked reason (degraded mode)
  - last error (if any)
- config/state schema markers
- pairing/lifecycle/update summaries
- cloud/network/node probes
- operational checks
- failure and attention summary
- recent coarse errors

Redacted by default (never exported):

- ingest API secret
- pairing code value
- activation token
- raw credential secret material

Identity and label fields are intentionally support-safe so multiple receivers can
be distinguished during troubleshooting.

Home Auto Session diagnostics are support-safe and do not include credential
secrets.

## Minimal Field Triage

1. Check portal **Troubleshooting** and **Progress** attention state.
2. Run `doctor` and record `failure_code` + `attention_state`.
3. Export `support-snapshot`.
4. If lifecycle is revoked/disabled/replaced, use reset and re-pair.
5. If version is unsupported, upgrade before further troubleshooting.

Detailed runbook:

- [Support and Operations Workflow](./support-operations-workflow.md)
