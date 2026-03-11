# Support and Operations Workflow (Field Guide)

This runbook focuses on common receiver field issues using local portal and CLI
tools only.

## Standard Data Capture

1. Run:
   - `loramapr-receiverd doctor -config /etc/loramapr/receiver.json`
2. Export:
   - `loramapr-receiverd support-snapshot -config /etc/loramapr/receiver.json -out /tmp/receiver-support.json`
3. Capture:
   - portal `/progress` screenshot
   - portal `/troubleshooting` screenshot

## Case: Receiver Offline in Cloud

Typical local indicators:

- failure code: `cloud_unreachable`
- operational checks:
  - `cloud_reachability = fail`
  - `service_health = ok|warn`

Actions:

1. Verify local internet/DNS/firewall.
2. Verify `cloud.base_url` in config.
3. Confirm system time is correct.
4. Recheck `doctor` and `status`.

## Case: Receiver Online but Node Missing

Typical local indicators:

- failure code: `no_serial_device_detected` or `node_detected_not_connected`
- operational check:
  - `node_connection = fail|warn`

Actions:

1. Check USB cable/power for Meshtastic device.
2. Verify serial device path/permissions.
3. Reconnect and confirm portal `/progress` node state changes.

## Case: Paired but No Packets Forwarding

Typical local indicators:

- failure code: `events_not_forwarding`
- operational checks:
  - `pairing_authorized = ok`
  - `forwarding_activity = fail|warn`

Actions:

1. Confirm cloud reachability and auth validity.
2. Confirm node traffic exists (not silent network).
3. Check ingest queue depth and last packet ack timestamps.
4. Re-pair only if auth/lifecycle indicators require it.

## Case: Receiver Replaced/Revoked/Disabled

Typical local indicators:

- failure code:
  - `receiver_credential_revoked`
  - `receiver_disabled`
  - `receiver_replaced`

Actions:

1. Use reset path:
   - portal `Reset And Re-pair`, or
   - `loramapr-receiverd reset-pairing -config /etc/loramapr/receiver.json`
2. Submit new pairing code.
3. Confirm `pairing_authorized` check becomes `ok`.

## Case: Local Schema/Upgrade Issue

Typical local indicators:

- failure code: `local_schema_incompatible`
- CLI hint indicates runtime/config/state schema mismatch

Actions:

1. Upgrade runtime to compatible version.
2. Re-run `doctor` and `support-snapshot`.
3. Avoid destructive state reset unless recovery policy requires it.

## Escalation Package

When escalating, include:

- `support-snapshot` JSON
- key failure code and operational summary
- install path (`linux-package`, `pi-appliance`, etc.)
- steps already attempted
