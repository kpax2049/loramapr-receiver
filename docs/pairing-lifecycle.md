# Receiver Pairing and Bootstrap Lifecycle

This document defines the receiver-side pairing lifecycle implemented in
`loramapr-receiverd` and aligned to the cloud runtime endpoints in
`../loramapr-cloud/src/onboarding/onboarding-receiver-runtime.controller.ts`.

## Cloud Contract (Current)

Receiver runtime endpoints used by the pairing client:

- `POST /api/receiver/bootstrap/exchange`
  - request: `{ "pairingCode": "LMR-..." }`
  - response includes:
    - `installSessionId`
    - `flowKey`
    - `activationToken`
    - `activationExpiresAt`
    - `activateEndpoint`
    - `heartbeatEndpoint`
    - `ingestEndpoint`
- `POST /api/receiver/activate`
  - request includes:
    - `activationToken`
    - optional runtime metadata (`runtimeVersion`, `platform`, `arch`, `label`, `metadata`)
  - response includes durable receiver credentials:
    - `receiverAgentId`
    - `ownerId`
    - `ingestApiKeyId`
    - `ingestApiKeySecret`
    - `heartbeatEndpoint`
    - `ingestEndpoint`

## State Machine

Persisted pairing phases (`state.PairingPhase`):

1. `unpaired`
2. `pairing_code_entered`
3. `bootstrap_exchanged`
4. `activated`
5. `steady_state`

## Transition Semantics

- `unpaired -> pairing_code_entered`
  - triggered by submitting pairing code (`POST /api/pairing/code` in local portal)
- `pairing_code_entered -> bootstrap_exchanged`
  - cloud pairing code exchange success
- `bootstrap_exchanged -> activated`
  - cloud activation success; durable ingest credentials persisted
- `activated -> steady_state`
  - local runtime confirms durable credentials are present

Failure transitions:

- retryable exchange/activation failure:
  - phase remains unchanged
  - retry is scheduled with backoff (`next_retry_at`, `retry_count`)
- non-retryable exchange/activation failure:
  - phase resets to `unpaired`
  - transient pairing values are cleared
- activation token expiration:
  - phase resets to `unpaired`

Lifecycle invalidation transitions (steady-state runtime):

- cloud credential revoked:
  - phase resets to `unpaired`
  - `pairing.last_change = credential_revoked`
  - durable cloud ingest credential is cleared locally
- receiver disabled:
  - phase resets to `unpaired`
  - `pairing.last_change = receiver_disabled`
  - durable cloud ingest credential is cleared locally
- receiver replaced/superseded:
  - phase resets to `unpaired`
  - `pairing.last_change = receiver_replaced`
  - durable cloud ingest credential is cleared locally

Local operator actions:

- local reset without deauthorization:
  - `pairing.last_change = local_reset`
- local reset with deauthorization (default reset command behavior):
  - `pairing.last_change = local_deauthorized`
  - durable cloud ingest credential is cleared locally

## Restart Safety

Pairing lifecycle is restart-safe via persisted state file:

- pairing code and bootstrap activation token are persisted while needed
- retry metadata (`retry_count`, `next_retry_at`) survives restarts
- durable cloud credentials survive restarts
- lifecycle invalidation (`credential_revoked`, `receiver_disabled`,
  `receiver_replaced`) survives restarts
- after restart, runtime resumes from persisted phase instead of restarting onboarding flow

## Sensitive Data Handling

Persisted but not exposed in status API/UI:

- `cloud.ingest_api_key_secret`
- `pairing.activation_token`
- `pairing.pairing_code`

`/api/status` only returns coarse state/health indicators and omits secret fields.
