# LoRaMapr Receiver v2.3.0 (Receiver Lifecycle Management)

Release date: 2026-03-11

## Highlights

- Added receiver lifecycle-aware runtime handling for cloud invalidation events:
  - `credential_revoked`
  - `receiver_disabled`
  - `receiver_replaced`
- Runtime now transitions to explicit local blocked state when lifecycle
  invalidation is detected:
  - clears durable ingest credentials
  - returns pairing phase to `unpaired`
  - stops active forwarding/heartbeat steady-state behavior
- Added explicit local reset/deauthorize operation:
  - CLI: `loramapr-receiverd reset-pairing`
  - portal: reset/re-pair action and JSON reset endpoint
- Expanded diagnostics failure taxonomy and portal guidance with lifecycle codes:
  - `receiver_credential_revoked`
  - `receiver_disabled`
  - `receiver_replaced`
- Added lifecycle semantics documentation for reinstall/reset/re-pair behavior.

## Runtime and Portal Changes

- New portal/API route: `POST /api/lifecycle/reset`
- New portal form route: `POST /reset`
- Pairing flow remains unchanged for happy path onboarding.
- Troubleshooting page now includes explicit lifecycle recovery action.

## Lifecycle Recovery Behavior

- Revoked/disabled/replaced credential responses now produce durable local state
  transitions and human-readable support-safe status.
- Recovery path is explicit: reset/deauthorize locally, then re-pair with a fresh
  code from cloud onboarding.

## Compatibility Notes

- Existing Linux/Pi OS package path and Pi appliance path remain supported.
- Package install/upgrade/remove behavior is unchanged.
- This release does not add auto-update; it hardens local lifecycle correctness.
