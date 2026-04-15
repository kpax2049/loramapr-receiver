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

`GET /api/ops` now includes:

- operational checks (`overall`, `checks[]`)
- derived attention object
- `setup_issues[]` (concrete first-run root causes with next-step guidance)

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

## Setup Root Cause Mapping

To avoid generic "degraded" setup wording, receiver derives concrete setup issues
from component status and operational checks.

Common first-run issue codes include:

- `portal_bind_localhost`
- `cloud_base_url_missing`
- `cloud_base_url_placeholder`
- `cloud_base_url_invalid`
- `cloud_unreachable`
- `usb_device_not_detected`
- `usb_detected_node_not_ready`
- `usb_protocol_unusable`
- `usb_serial_permission_denied`
- `packets_not_ingesting`

State-store safety notes:

- If `/var/lib/loramapr/receiver-state.json` is corrupted (for example after an
  unclean power loss), receiver now auto-recovers on startup.
- The corrupt file is backed up as
  `/var/lib/loramapr/receiver-state.json.corrupt-<timestamp>`.
- Receiver then regenerates a clean state file so portal/service can come back
  instead of crash-looping.

These are surfaced on:

- Portal **Welcome**, **Progress**, and **Troubleshooting**
- `GET /api/ops` as `setup_issues[]`
- `support-snapshot` export under `setup.issues`

## Support Snapshot Export

`support-snapshot` includes support-safe context:

- runtime version/channel/build/platform/install type
- identity context:
  - installation id
  - local name and hostname hints
  - cloud receiver label/id and optional cloud labels (when present)
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
- Meshtastic field-node pairing context:
  - home-node config summary availability
  - region / primary channel / PSK state
  - LoRa summary fields when reported
  - share URL availability + redacted share hint
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
- Meshtastic raw share URL / QR text

Identity and label fields are intentionally support-safe so multiple receivers can
be distinguished during troubleshooting.
Cloud `site/group` labels are optional metadata only; missing labels are not a
setup failure.

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

## Tick and Retry Logs

For intermittent stale/online drift issues, inspect service logs for tick-level
signals:

- `status tick processed`: periodic runtime status evaluation completed
- `heartbeat tick skipped`: heartbeat intentionally not sent (for example not in
  `steady_state` or missing credentials)
- `heartbeat tick sent`: periodic heartbeat request completed
- `heartbeat tick failed`: heartbeat request failed (`retryable=true|false`)
- `ingest retry scheduled`: retry/backoff selected for queue delivery failure

### Temporary Ingest Timing Trace

Enable detailed per-event ingest timing logs (for root-cause analysis only):

```bash
sudo systemctl edit loramapr-receiverd
# add:
# [Service]
# Environment=LORAMAPR_INGEST_TRACE=1
sudo systemctl daemon-reload
sudo systemctl restart loramapr-receiverd
sudo journalctl -u loramapr-receiverd -f --no-pager | grep "ingest pipeline trace"
```

Trace fields include:

- `capturedAt`
- `enqueuedAt`
- `dequeuedAt` / `sendAttemptAt`
- `ackedAt`
- `statusCode` / `errorCode`
- `lagCapturedTo*Ms`, `lagEnqueuedTo*Ms`
- `queueDepth`

Home Auto Session control loop logs:

- `home auto session decision update`
- `home auto session cloud action request`
- `home auto session cloud action response`
- `home auto session retry scheduled`
- `home auto session stop fallback activated`
- `home auto session start conflict retry scheduled`
- `home auto session action blocked by lifecycle conflict`
- `home auto session action blocked by non-retryable cloud error`
- failed action logs include:
  - `action`, `endpoint`, `attempt`
  - `status_code`, `error_code`
  - `cloud_request_id` (when returned by cloud)
  - `session_id_included` (`true`/`false`)
- For stop retries caused by network/timeout errors, look for:
  - `retry_class=timeout_network`
  - summary text: `stop pending; cloud unreachable/slow (...)`
  - decision text including retry attempt count and next retry ETA
- For cloud start conflicts missing a session ID, look for:
  - `error_class=has_start_missing_session_id_conflict`
  - `dedupe_key_hash` + `next_retry_at` fields
  - control state transitioning through temporary conflict/cooldown before retry
