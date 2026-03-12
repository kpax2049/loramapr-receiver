# Multi-Receiver Identity and Local Guidance

This document explains how LoRaMapr Receiver identifies itself and presents
coexistence/replacement context when multiple receivers are deployed.

## Identity Signals Reported by Receiver

Receiver runtime exposes support-safe identity hints through heartbeat/status,
portal, doctor output, and support snapshot export.

Primary fields:

- `installation_id`: durable local install identity
- `local_name`: local operator hint (`runtime.local_name` or derived default)
- `hostname`: local runtime host hint
- `cloud_receiver_id`: cloud receiver identifier when paired
- `cloud_receiver_label`: cloud-provided receiver display label (if available)
- `cloud_site_label`: cloud-provided site hint (if available)
- `cloud_group_label`: cloud-provided group hint (if available)
- install/runtime context:
  - install type
  - platform/arch
  - version/channel/build

## Local Naming Behavior

`local_name` precedence:

1. `runtime.local_name` from config
2. persisted prior `installation.local_name`
3. derived value from hostname/install type plus installation suffix

`local_name` is sanitized to remove unsupported control/punctuation characters.

## Cloud Metadata Reflection

Cloud remains source of truth for ownership and grouping. Receiver only reflects
cloud-provided identity labels when present:

- bootstrap exchange
- activation response
- heartbeat acknowledgement

When lifecycle reset/revocation clears durable cloud credentials, receiver also
clears cloud identity labels to avoid stale display.

## Local Guidance for Coexistence and Replacement

Portal troubleshooting and next-action guidance now explicitly covers:

- adding this receiver as an additional receiver
- this receiver replacing another receiver
- this receiver being replaced/revoked/disabled
- node not visible on this receiver due to attachment elsewhere

## What Receiver Does Not Manage

Receiver does not implement local team/group management. It does not decide
ownership, policy, or canonical grouping. Those stay cloud-managed.

## Operator Checklist in Multi-Receiver Deployments

1. Verify local portal identity chips (`local_name`, `cloud_receiver_label`).
2. Confirm site/group labels match intended deployment context.
3. If receiver is replaced/revoked, use reset-and-re-pair flow.
4. If node appears missing, verify physical attachment on this specific receiver.
