# Support and Troubleshooting Workflow

This is the primary field runbook for LoRaMapr Receiver support.

It applies to the supported install path:

- Existing Debian-family Linux / Raspberry Pi OS package install

Receiver appliance image path is deprecated/paused.

## 1. Identify Install Path and Device Access

Record which path is in use:

- `linux-package`
- `manual-systemd` (advanced fallback)

Confirm operator can open local portal:

- `http://loramapr-receiver.local:8080` (preferred)
- `http://<device-lan-ip>:8080` (fallback)

## 2. Collect Standard Support Data

Run:

```bash
loramapr-receiverd doctor -config /etc/loramapr/receiver.json
loramapr-receiverd support-snapshot -config /etc/loramapr/receiver.json -out /tmp/receiver-support.json
```

Capture:

- portal `/progress` screenshot
- portal `/troubleshooting` screenshot
- setup root causes from portal **Progress** (`Setup Root Cause`) or
  `GET /api/ops` -> `setup_issues[]`
- identity hints:
  - `installation_id`
  - `local_name`
  - `cloud_receiver_label`
  - optional `cloud_site_label` / `cloud_group_label` (if present)
- attention fields:
  - `attention_state`
  - `attention_code`
  - `attention_hint`

## 3. Common Failure Flows

### Receiver Offline in Cloud

Indicators:

- failure: `cloud_unreachable`
- attention: `action_required` / `connectivity`
- operational: `cloud_reachability = fail`
- setup root causes may include:
  - `cloud_base_url_missing`
  - `cloud_base_url_placeholder`
  - `cloud_base_url_invalid`
  - `cloud_unreachable`

Actions:

1. Verify local internet/DNS/firewall.
2. Verify `cloud.base_url`.
   - update without manual file editing:
     - `sudo /usr/bin/loramapr-receiverd configure-cloud -config /etc/loramapr/receiver.json -base-url http://<cloud-host-or-ip>:3001`
     - `sudo systemctl restart loramapr-receiverd`
3. Confirm system clock is correct.
4. Recheck portal and `doctor` output.

### Receiver Online but Node Missing

Indicators:

- failure: `no_serial_device_detected` or `node_detected_not_connected`
- attention: `action_required` / `node`
- setup root causes may include:
  - `usb_device_not_detected`
  - `usb_detected_node_not_ready`
  - `usb_protocol_unusable`
  - `usb_serial_permission_denied`

Actions:

1. Check USB cable/power to Meshtastic device.
2. Confirm serial device permissions/path.
3. In multi-receiver setups, verify the node is attached to this receiver, not a different one.
4. Refresh portal Progress for node state update.

### Field-Node Pairing Data Missing

Indicators:

- Progress page shows field-node pairing data unavailable
- Meshtastic config summary shows `available=false`

Actions:

1. Confirm Meshtastic adapter is `connected`.
2. Wait for node status/config events to populate summary fields.
3. If share URL is still unavailable, use manual region/channel summary.
4. Fallback to Meshtastic app native share flow if node does not emit share URL.

### Paired but Not Forwarding

Indicators:

- failure: `events_not_forwarding`
- attention: `action_required` / `forwarding`
- operational: `pairing_authorized = ok`, `forwarding_activity = fail|warn`

Actions:

1. Verify cloud reachability.
2. Confirm node is producing traffic.
3. Check queue depth and last packet acknowledgement.
4. Re-pair only if auth/lifecycle state requires it.

### Home Auto Session Not Starting/Stopping

Indicators:

- Home Auto Session state: `misconfigured`, `cooldown`, `degraded`,
  `start_pending`, or `stop_pending`
- Home Auto Session control state: `conflict_blocked` or `lifecycle_blocked`
- Home Auto Session reconciliation state indicates startup recovery path
- Home Auto Session active-state source indicates whether state is cloud-acknowledged
  or recovered/conflicted
- Home Auto Session last error/decision indicates cloud/session control issue
- Home Auto Session GPS status indicates whether geofence decisions are usable

Actions:

1. Open portal `/home-auto-session` and verify geofence + tracked nodes config.
2. Confirm mode (`observe` vs `control`) matches expected behavior.
3. In control mode, verify receiver is paired and has valid cloud credentials.
4. Use **Reevaluate Now** after config fixes.
5. If stuck degraded/cooldown, use **Reset Degraded/Cooldown State** and re-test.
6. If state is `boundary_uncertain` or `stale`, wait for stable fresh GPS before expecting auto start/stop.
7. If control state is `lifecycle_blocked`, reset/re-pair before further control testing.

### Revoked, Disabled, or Replaced

Indicators:

- failure: `receiver_credential_revoked` / `receiver_disabled` / `receiver_replaced`
- attention: `urgent` / `lifecycle`

Actions:

1. Use **Reset And Re-pair** in portal Troubleshooting (preferred).
2. Or run:

```bash
loramapr-receiverd reset-pairing -config /etc/loramapr/receiver.json
```

3. Submit fresh pairing code.

If replacement was intentional, confirm receiver identity labels (and optional
cloud site/group labels if present) match expected cloud assignment after
re-pair.

### Outdated or Unsupported Version

Indicators:

- failure: `receiver_outdated` or `receiver_version_unsupported`
- attention: `action_required|urgent` / `version`

Actions:

1. Upgrade via supported package release path (APT or manual `.deb` fallback).
2. Recheck portal attention and operational status after restart.

## 4. Path-Specific Notes

Existing Linux/Pi OS:

- verify service health with `systemctl status loramapr-receiverd`
- confirm package lifecycle behavior (`remove` vs `purge`) before destructive actions

## 5. Escalation Package

Include in escalation:

- `/tmp/receiver-support.json`
- install path (`linux-package` or `manual-systemd`)
- failure + attention summary
- actions already attempted
