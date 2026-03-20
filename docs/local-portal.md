# Embedded Local Setup Portal

`loramapr-receiverd` serves a local web portal for setup, status, and
troubleshooting.

For most users, this portal is the only interface needed after install.

## Open the Portal

Portal address depends on network context:

- Linux/Pi OS host on LAN: `http://loramapr-receiver.local:8080` (preferred)
- fallback: `http://<device-lan-ip>:8080`
- local desktop/dev install: `http://127.0.0.1:8080`

Bind address is configured by `portal.bind_address`.

## What You Do in the Portal

1. **Welcome**: confirm current setup/attention state and next action
2. **Pairing**: enter pairing code from LoRaMapr Cloud
3. **Progress**: confirm cloud reachability, node connection, forwarding, and
   read-only field-node pairing data from the connected home node, plus concrete
   setup root-cause issues when setup is degraded
4. **Home Auto Session**: optional embedded session automation with cloud-managed
   policy visibility and local fallback config
5. **Troubleshooting**: resolve common issues and run reset/re-pair when needed
6. **Advanced**: build/install/identity metadata for support and diagnostics

Identity shown in portal includes:

- local receiver name hint
- hostname hint
- cloud receiver label/id when available
- optional cloud labels (`site`/`group`) when available

## Routes

UI routes:

- `GET /`
- `GET /pairing`
- `POST /pairing`
- `GET /progress`
- `GET /home-auto-session`
- `POST /home-auto-session`
- `POST /home-auto-session/reevaluate`
- `POST /home-auto-session/reset`
- `GET /troubleshooting`
- `POST /reset`
- `GET /advanced`

API routes:

- `GET /healthz`
- `GET /readyz`
- `GET /api/status`
- `GET /api/events/status` (SSE status stream)
- `GET /api/ops`
- `POST /api/pairing/code`
- `POST /api/lifecycle/reset`

`GET /api/ops` includes:

- coarse operational check summary
- derived attention object (`state/category/code/summary/hint`)
- `setup_issues[]` root-cause list for first-run blockers

Portal pages (`/`, `/progress`, `/troubleshooting`, `/advanced`) subscribe to
`/api/events/status` and auto-refresh only when status changes. Fallback refresh
is low-rate and only used if SSE is unavailable.

## Attention States (User Meaning)

- `none`: receiver is healthy enough for current mode
- `info`: keep an eye on status, no immediate intervention
- `action_required`: local action needed to recover normal operation
- `urgent`: blocking issue; recover now (for example revoked/replaced/unsupported)

Common causes you may see:

- `pairing_not_completed`
- `cloud_unreachable`
- `no_serial_device_detected`
- `events_not_forwarding`
- `receiver_credential_revoked`
- `receiver_version_unsupported`

Home Auto Session module states you may see:

- `disabled`
- `misconfigured`
- `observe_ready`
- `control_ready`
- `start_pending`
- `active`
- `stop_pending`
- `cooldown`
- `degraded`

Home Auto Session production control details now include:

- control state (`ready`, `pending_start`, `pending_stop`, `conflict_blocked`,
  `lifecycle_blocked`, etc.)
- active state source (`cloud_acknowledged`, `local_recovered_unverified`,
  `conflict_unresolved`)
- reconciliation state (startup recovery outcome)
- pending action (`start|stop`) during retry/recovery
- tracked node state summary
- last action and last action result
- GPS validity state (`missing|invalid|stale|boundary_uncertain|valid`)
- blocked reason for degraded mode
- effective config source/version
- cloud-config present state
- last config apply result/error
- desired config enabled/mode hints

Cloud-managed config wording in portal covers:

- using cloud-managed config
- no cloud config available (local fallback active)
- cloud config invalid and not applied
- cloud config disables module
- desired config differs from runtime due to blocked/degraded state

Typical plain-language hints:

- waiting for tracked node near home geofence transition
- would start session now, but observe mode is enabled
- session active
- waiting for node to return home
- cloud/session API unavailable
- recovering pending start/stop action after restart
- waiting for stable position near geofence boundary
- control is blocked due to cloud/local conflict
- control is blocked because receiver was revoked/disabled/replaced

Optional multi-receiver hints may appear, but are not required for setup:

- this receiver replaced another receiver
- this receiver has been replaced/revoked/disabled
- node is attached elsewhere and not currently seen by this receiver

## Security and Privacy

Portal intentionally omits secret material, including:

- pairing code value
- activation token
- ingest API key secret

Meshtastic share URLs can include channel secrets. They are shown only on local
portal surfaces and should be used on trusted local networks/devices.

Only support-safe metadata is shown.

## If Setup Is Stuck

1. Open **Troubleshooting** and follow suggested actions.
2. Run local diagnostics:

```bash
loramapr-receiverd doctor -config /etc/loramapr/receiver.json
loramapr-receiverd support-snapshot -config /etc/loramapr/receiver.json -out /tmp/receiver-support.json
```

3. Continue with:

- [Support and Operations Workflow](./support-operations-workflow.md)
