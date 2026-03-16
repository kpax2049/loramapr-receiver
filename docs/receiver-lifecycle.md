# Receiver Lifecycle Management

This document defines receiver-side lifecycle behavior for `v2.3.0`.

## Lifecycle Triggers

Receiver lifecycle transitions are driven by cloud responses from steady-state
heartbeat and ingest paths.

Cloud-indicated lifecycle events:

- `credential_revoked`
- `receiver_disabled`
- `receiver_replaced`

## Local Behavior on Lifecycle Invalidation

When one of the lifecycle events is detected, receiver runtime now:

1. transitions local pairing state to `unpaired`
2. persists `pairing.last_change` with lifecycle code
3. clears durable ingest credentials locally
4. stops active ingest/heartbeat steady-state behavior
5. marks service as lifecycle-blocked in runtime status
6. exposes actionable guidance in portal/diagnostics

This is restart-safe because lifecycle transition is written to persisted local
state before next tick.

## Local Reset and Re-pair

Receiver supports explicit local lifecycle operations:

- CLI:
  - `loramapr-receiverd reset-pairing -config /etc/loramapr/receiver.json`
  - default behavior is deauthorization (`-deauthorize=true`)
- Portal:
  - `POST /reset` (form flow)
  - `POST /api/lifecycle/reset` (JSON flow)

Reset/deauthorize returns receiver to unpaired setup flow and keeps installation
layout intact.

## Lifecycle State Mapping

Persisted indicators:

- `pairing.phase = unpaired`
- `pairing.last_change` set to one of:
  - `credential_revoked`
  - `receiver_disabled`
  - `receiver_replaced`
  - `local_reset`
  - `local_deauthorized`

Runtime/portal diagnostics map these to coarse failure codes:

- `receiver_credential_revoked`
- `receiver_disabled`
- `receiver_replaced`

## Reinstall and Identity Semantics

### Existing Linux/Pi OS package path

- package upgrade/reinstall preserves:
  - `installation.id`
  - local state/config (unless explicit purge/reset)
- `reset-pairing` deauthorizes receiver identity but does not regenerate
  `installation.id`

### Pi appliance path (deprecated)

Receiver appliance image path is deprecated/paused. Existing historical
appliance installs follow the same state semantics:

- same SD card + existing state: install identity persists
- fresh SD card/image: new install identity generated; pairing required

### Replacement Meaning (Local)

`receiver_replaced` means this local runtime credential has been superseded by a
different active receiver in cloud. This host can become active again only by
explicit reset + re-pair.
