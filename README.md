# LoRaMapr Receiver

LoRaMapr Receiver is the local runtime that connects a Meshtastic node to
LoRaMapr Cloud.

It runs as a background service (`loramapr-receiverd`), exposes a local setup
portal, and forwards packets to the existing cloud ingest path after pairing.

## Choose Your Install Path

LoRaMapr Receiver supports two public install paths.

1. Raspberry Pi Appliance (recommended for most users)
   - Flash the LoRaMapr Receiver Pi image, boot, open local portal, pair.
   - Guide: [Raspberry Pi Appliance Path](./docs/raspberry-pi-appliance.md)
2. Existing Debian-family Linux / Raspberry Pi OS
   - Install `loramapr-receiver` from the signed APT repository.
   - Guide: [Linux/Pi Existing-OS Path](./docs/linux-pi-distribution.md)

Advanced fallback:

- Manual systemd-layout tarball install (for constrained environments only).

## Pairing and First Run

After install, the receiver enters pairing-ready mode and waits for a pairing
code from LoRaMapr Cloud.

1. Open the local portal:
   - Pi appliance: `http://loramapr-receiver.local:8080`
   - fallback: `http://<device-lan-ip>:8080`
2. Go to **Pairing**.
3. Paste the pairing code from LoRaMapr Cloud.
4. Wait until the portal shows paired/ready state.
5. Confirm Meshtastic node connection and forwarding on **Progress**.

Portal reference: [Embedded Local Setup Portal](./docs/local-portal.md)

For households/teams with multiple receivers, see
[Multi-Receiver Identity and Guidance](./docs/multi-receiver-identity.md).

Optional automation module:

- [Embedded Home Auto Session (Milestone 3)](./docs/home-auto-session.md)

## Local Attention States

The portal and diagnostics show one attention state:

- `none`: no action required
- `info`: informational or early warning
- `action_required`: receiver needs local action to recover
- `urgent`: blocking issue (for example revoked/replaced/unsupported)

Diagnostics reference: [Receiver Diagnostics](./docs/diagnostics.md)

## If Setup Fails

Collect local support information:

```bash
loramapr-receiverd doctor -config /etc/loramapr/receiver.json
loramapr-receiverd support-snapshot -config /etc/loramapr/receiver.json -out /tmp/receiver-support.json
```

Then follow the field workflow:

- [Support and Operations Workflow](./docs/support-operations-workflow.md)

## Documentation

Start here:

- [Docs Index](./docs/README.md)
- [Reviewer Smoke Test Guide](./docs/reviewer-smoke-test.md)
- [Release Notes](./docs/release-notes.md)

## Maintainer/Developer Notes

Repository layout:

- `cmd/loramapr-receiverd`: runtime entrypoint
- `internal/`: runtime subsystems (config/state/cloud/portal/adapter/runtime)
- `docs/`: product and operational documentation
- `packaging/`: release, package, distribution, and image scaffolding

Build from source:

```bash
go build -o bin/loramapr-receiverd ./cmd/loramapr-receiverd
```
