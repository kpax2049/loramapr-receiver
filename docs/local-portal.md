# Embedded Local Setup Portal

`loramapr-receiverd` serves an embedded local web portal for first-run setup and
ongoing receiver status checks.

## Purpose

The portal is the normal human interface for receiver bootstrap:

- enter pairing code from LoRaMapr Cloud
- monitor pairing/bootstrap progress
- view runtime health/readiness
- view practical troubleshooting guidance
- inspect non-sensitive advanced runtime details

## Routes

UI routes:

- `GET /` welcome + quick status
- `GET /pairing` pairing code entry form
- `POST /pairing` pairing code submission form action
- `POST /reset` local reset/deauthorize form action
- `GET /progress` setup/runtime progress
- `GET /troubleshooting` human-readable checks
- `GET /advanced` runtime/platform details

API routes:

- `GET /healthz` liveness
- `GET /readyz` readiness
- `GET /api/status` structured status JSON
- `POST /api/pairing/code` JSON pairing submission
- `POST /api/lifecycle/reset` JSON reset/deauthorize action

## Binding Strategy

Configured by `portal.bind_address` in receiver config.

Recommended defaults:

- desktop/local install path: `127.0.0.1:8080`
- Raspberry Pi/appliance path: `0.0.0.0:8080`

Use loopback by default on general-purpose hosts; expose on LAN only for
appliance-style usage where setup happens from another device.

Appliance discovery assumptions:

- preferred hostname URL: `http://loramapr-receiver.local:8080`
- fallback URL: `http://<lan-ip>:8080`

## Security Behavior

Portal intentionally omits sensitive values from rendered pages and `/api/status`:

- ingest API secret
- activation token
- persisted pairing code

Those remain in local state storage with restricted file permissions.

## Diagnostics Integration

Portal pages now surface coarse receiver failure taxonomy with actionable hints:

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

This keeps setup failures human-readable while staying aligned with cloud/onboarding
terminology.

Lifecycle recovery path in portal:

- troubleshooting page includes "Reset And Re-pair" action
- reset transitions local runtime to `unpaired` setup state
- user then submits a fresh pairing code on the Pairing page
