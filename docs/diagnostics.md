# Receiver Diagnostics and Failure Taxonomy

This document defines first-run/runtime diagnostics behavior for
`loramapr-receiverd`.

## Failure Taxonomy

Diagnostics use coarse, human-readable failure codes:

- `pairing_code_invalid`
- `pairing_code_expired`
- `activation_failed`
- `pairing_not_completed`
- `receiver_credential_revoked`
- `receiver_disabled`
- `receiver_replaced`
- `cloud_unreachable`
- `network_unavailable`
- `portal_unavailable`
- `receiver_auth_invalid`
- `no_serial_device_detected`
- `node_detected_not_connected`
- `events_not_forwarding`
- `cloud_config_incompatible`

Status surfaces expose:

- `failure_code`
- `failure_summary`
- `failure_hint`
- `failure_since`
- `recent_failures`

## Portal and CLI Surfaces

Portal:

- Welcome/Progress/Troubleshooting render failure summary + actionable hint.
- Troubleshooting includes lifecycle reset/re-pair guidance when applicable.

CLI:

- `doctor`: human or JSON summary with failure code/hint.
- `status`: JSON status snapshot including failure/update metadata.
- `support-snapshot`: redacted support bundle.

## Update and Upgrade Visibility in Diagnostics

Diagnostics also surface update-status reasoning:

- `update_status`
- `update_summary`
- `update_hint`
- recommended/manifest version metadata
- last update check timestamp

Cloud-config compatibility failures are explicit and map to:

- `cloud_config_incompatible` failure taxonomy
- runtime blocked state until compatible version or runtime upgrade

## Support Snapshot

Generate:

```bash
loramapr-receiverd support-snapshot \
  -config /etc/loramapr/receiver.json \
  -out /tmp/receiver-support.json
```

Snapshot includes:

- runtime: version/channel/commit/build date/build id/platform/arch/install type
- pairing phase and coarse lifecycle/error metadata
- cloud reachability summary
- network/portal bind summary
- meshtastic detection summary
- update-status summary
- active failure summary + recent coarse errors

## Redaction Guarantees

Support snapshot does not include secret values:

- `cloud.ingest_api_key_secret`
- `pairing.pairing_code`
- `pairing.activation_token`

Only boolean presence indicators are exported for these fields.

## Support Workflow

1. Run `doctor` and capture failure/update codes.
2. If lifecycle blocked, run local reset/re-pair:
   - `loramapr-receiverd reset-pairing -config /etc/loramapr/receiver.json`
3. Export `support-snapshot` JSON.
4. Confirm portal Troubleshooting output matches diagnostics taxonomy.
5. Escalate with failure code + redacted snapshot.
