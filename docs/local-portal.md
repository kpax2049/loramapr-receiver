# Embedded Local Setup Portal

`loramapr-receiverd` serves an embedded local web portal for first-run setup,
runtime visibility, lifecycle recovery, and update-status visibility.

## Purpose

The portal is the normal operator interface for:

- entering pairing code from LoRaMapr Cloud
- monitoring bootstrap/activation progress
- checking service/cloud/meshtastic readiness
- understanding actionable failure states
- viewing non-sensitive advanced runtime/build details

## Routes

UI:

- `GET /` welcome + summary
- `GET /pairing` pairing form
- `POST /pairing` pairing submit
- `POST /reset` local reset/deauthorize
- `GET /progress` setup/runtime progress
- `GET /troubleshooting` human-readable guidance
- `GET /advanced` runtime/build/install details

API:

- `GET /healthz`
- `GET /readyz`
- `GET /api/status`
- `POST /api/pairing/code`
- `POST /api/lifecycle/reset`

## Binding Strategy

Configured by `portal.bind_address`.

Recommended defaults:

- desktop/local host path: `127.0.0.1:8080`
- Pi appliance path: `0.0.0.0:8080`

Discovery assumptions for appliance profile:

- preferred: `http://loramapr-receiver.local:8080`
- fallback: `http://<lan-ip>:8080`

## Security Model

Portal pages and `/api/status` intentionally omit secrets:

- ingest API secret
- activation token
- pairing code value

Secret-bearing fields remain only in local state storage with restricted file
permissions.

## Status and Diagnostics Integration

Progress/Troubleshooting pages surface:

- pairing phase
- cloud connectivity/reachability
- service lifecycle/readiness
- meshtastic state
- update status (`disabled`, `unknown`, `current`, `outdated`, `channel_mismatch`, `unsupported`, `ahead`)
- failure code/summary/hint

Advanced page surfaces:

- version/channel
- build commit/date/id
- platform/arch
- install type/profile/mode
- update manifest/recommendation/check timestamp

## Failure Taxonomy Visibility

Portal aligns with diagnostics taxonomy codes, including:

- pairing/bootstrap: `pairing_code_invalid`, `pairing_code_expired`, `activation_failed`, `pairing_not_completed`
- lifecycle: `receiver_credential_revoked`, `receiver_disabled`, `receiver_replaced`
- connectivity/runtime: `cloud_unreachable`, `network_unavailable`, `portal_unavailable`, `receiver_auth_invalid`
- meshtastic/forwarding: `no_serial_device_detected`, `node_detected_not_connected`, `events_not_forwarding`
- upgrade compatibility: `cloud_config_incompatible`

## Lifecycle Recovery Path

- Troubleshooting page includes reset/re-pair action.
- Reset deauthorizes durable credentials by default.
- Receiver returns to `unpaired` and user submits fresh pairing code.

## Update Status Scope

Portal update-state display is informational only:

- no automatic update install
- no background package/image mutation
- manual upgrade path remains package/appliance release workflow
