# Receiver Diagnostics and Failure Taxonomy

This document defines first-run diagnostics behavior for LoRaMapr Receiver.

## Failure Taxonomy

Runtime/portal diagnostics now use these coarse failure codes:

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

These codes are exposed in `/api/status` as:

- `failure_code`
- `failure_summary`
- `failure_hint`
- `failure_since`
- `recent_failures`

## Portal Behavior

The local portal surfaces failure code/summary/hint on:

- Welcome
- Progress
- Troubleshooting

Troubleshooting page keeps guidance human-readable and avoids raw debug dumps.

## Doctor Command

`doctor` now reports diagnostics with taxonomy-aware output.

Human-readable:

```bash
loramapr-receiverd doctor -config /etc/loramapr/receiver.json
```

JSON report:

```bash
loramapr-receiverd doctor -config /etc/loramapr/receiver.json -json
```

## Support Snapshot Export

Use `support-snapshot` to export a redacted support bundle:

```bash
loramapr-receiverd support-snapshot -config /etc/loramapr/receiver.json -out /tmp/receiver-support.json
```

Snapshot includes:

- runtime version/platform
- pairing/activation state
- cloud reachability probe summary
- local network availability probe summary
- meshtastic device detection summary
- active failure code/summary/hint
- recent coarse errors

Redaction guarantees:

- does **not** include ingest API secret
- does **not** include pairing code value
- does **not** include activation token value

Only boolean presence indicators are included for secret-bearing fields.

## Support Workflow

1. Run `doctor` and note failure code + hint.
2. If failure is lifecycle-related (`receiver_credential_revoked`,
   `receiver_disabled`, `receiver_replaced`), run local reset/deauthorize:
   - `loramapr-receiverd reset-pairing -config /etc/loramapr/receiver.json`
3. Capture `support-snapshot` JSON.
4. Confirm portal Troubleshooting output matches failure code.
5. If cloud-side escalation is needed, share failure code and redacted snapshot.

This keeps terminology aligned between local receiver support and cloud onboarding
status surfaces.

## Package Lifecycle Troubleshooting

For Debian-family package lifecycle issues:

1. Check package/service status:
   - `systemctl status loramapr-receiverd --no-pager`
   - `dpkg -s loramapr-receiver`
2. Check config/state preservation expectations in:
   - `docs/linux-package-lifecycle.md`
3. Confirm install mode path:
   - `.deb`/APT path is primary
   - tarball/systemd extraction is fallback/advanced

When escalating support, include whether issue occurred during install, upgrade,
remove, purge, or tarball-to-package migration.
