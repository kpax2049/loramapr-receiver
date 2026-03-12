# Embedded Local Setup Portal

`loramapr-receiverd` serves a local web portal for setup, status, and
troubleshooting.

For most users, this portal is the only interface needed after install.

## Open the Portal

Portal address depends on install path:

- Pi appliance: `http://loramapr-receiver.local:8080` (preferred)
- fallback: `http://<device-lan-ip>:8080`
- local desktop/dev install: `http://127.0.0.1:8080`

Bind address is configured by `portal.bind_address`.

## What You Do in the Portal

1. **Welcome**: confirm current setup/attention state and next action
2. **Pairing**: enter pairing code from LoRaMapr Cloud
3. **Progress**: confirm cloud reachability, node connection, and forwarding
4. **Home Auto Session**: optional local geofence-based session automation config
5. **Troubleshooting**: resolve common issues and run reset/re-pair when needed
6. **Advanced**: build/install/identity metadata for support and diagnostics

Identity shown in portal includes:

- local receiver name hint
- hostname hint
- cloud receiver label/id when available
- cloud site/group labels when available

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
- `GET /api/ops`
- `POST /api/pairing/code`
- `POST /api/lifecycle/reset`

`GET /api/ops` includes:

- coarse operational check summary
- derived attention object (`state/category/code/summary/hint`)

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

Typical plain-language hints:

- waiting for tracked node near home geofence transition
- would start session now, but observe mode is enabled
- session active
- waiting for node to return home
- cloud/session API unavailable

In multi-receiver environments, portal guidance also helps distinguish:

- this receiver is an additional receiver
- this receiver replaced another receiver
- this receiver has been replaced/revoked/disabled
- node is attached elsewhere and not currently seen by this receiver

## Security and Privacy

Portal intentionally omits secret material, including:

- pairing code value
- activation token
- ingest API key secret

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
