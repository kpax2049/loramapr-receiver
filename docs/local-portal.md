# Embedded Local Setup Portal

`loramapr-receiverd` serves an embedded local web portal for setup, status, and
support-safe troubleshooting.

## Purpose

Portal is the normal operator interface for:

- pairing/bootstrap progress
- service/cloud/node status
- lifecycle recovery guidance
- update-supportability visibility
- coarse operational checks

## Routes

UI:

- `GET /` welcome/summary
- `GET /pairing` pairing code form
- `POST /pairing` pairing submission
- `POST /reset` local reset/deauthorize
- `GET /progress` runtime progress + operational checks
- `GET /troubleshooting` actionable guidance
- `GET /advanced` build/runtime/install details

API:

- `GET /healthz`
- `GET /readyz`
- `GET /api/status`
- `GET /api/ops` (coarse operational checks)
- `POST /api/pairing/code`
- `POST /api/lifecycle/reset`

## Operational Checks in Portal

Progress/Troubleshooting surfaces include check results for:

- service health/running
- pairing authorized
- cloud reachability
- node connection state
- forwarding recent activity
- update supportability state

Each check uses coarse levels:

- `ok`
- `warn`
- `fail`
- `unknown`

Overall operational state:

- `ok`
- `degraded`
- `blocked`

## Security Model

Portal intentionally omits secrets from rendered content and `/api/status`:

- pairing code value
- activation token
- ingest API secret

Only support-safe metadata is exposed.

## Troubleshooting Guidance

Portal guidance aligns to stable receiver taxonomy:

- pairing/setup: `pairing_code_invalid`, `pairing_code_expired`, `activation_failed`, `pairing_not_completed`
- lifecycle/auth: `receiver_credential_revoked`, `receiver_disabled`, `receiver_replaced`, `receiver_auth_invalid`
- connectivity/runtime: `cloud_unreachable`, `network_unavailable`, `portal_unavailable`, `cloud_config_incompatible`, `local_schema_incompatible`
- node/forwarding: `no_serial_device_detected`, `node_detected_not_connected`, `events_not_forwarding`
- release supportability: `receiver_outdated`, `receiver_version_unsupported`

## Binding and Discovery

Configured by `portal.bind_address`.

Recommended:

- desktop/local: `127.0.0.1:8080`
- Pi appliance: `0.0.0.0:8080`

Pi discovery assumptions:

- preferred: `http://loramapr-receiver.local:8080`
- fallback: `http://<lan-ip>:8080`
