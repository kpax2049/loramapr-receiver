# LoRaMapr Receiver Simplification Cleanup Notes

Date: 2026-03-14

## Summary

This cleanup removes receiver-side wording and UX assumptions that
owner/workspace/site/group concepts are required for onboarding.

The normal receiver path remains:

1. install
2. pair
3. verify cloud and node state
4. confirm forwarding

## Changes

- Portal guidance now focuses on direct pairing and recovery, without required
  household/team/workspace language.
- Progress/Advanced pages now treat cloud `site/group` labels as optional
  metadata.
- `doctor` output now labels cloud site/group values as optional cloud labels.
- Pairing and runtime docs now mark `ownerId`, `siteLabel`, and `groupLabel` as
  optional cloud metadata.
- Support/reviewer docs now explicitly state optional identity labels are
  non-blocking.

## Compatibility

- No pairing/bootstrap protocol changes.
- No changes to credential storage format.
- No changes to ingest/heartbeat forwarding behavior.
