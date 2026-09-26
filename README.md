# LoRaMapr Receiver

LoRaMapr Receiver is the local runtime that connects radio devices to
LoRaMapr Cloud. It supports Meshtastic and MeshCore Companion connections;
LoRaWAN devices connect to Cloud through their network-server webhook path.

It runs as a background service (`loramapr-receiverd`), exposes a local setup
portal, and forwards observations to Cloud after pairing.

## Supported radio ecosystems

- **Meshtastic** — receiver-connected packet ingest and fixed-base coverage
  workflows.
- **MeshCore** — a MeshCore Companion connected to Receiver over BLE for
  Session-managed telemetry, normal Device/Session position workflows, and
  retained evidence provenance.
- **LoRaWAN** — Cloud supports The Things Stack webhooks; it does not require
  a Receiver radio connection.

## MeshCore Companion

Use a MeshCore Companion running stock/official Companion firmware with a
BLE-capable Linux or Raspberry Pi Receiver. Pair the Receiver to Cloud, connect
the Companion over BLE, and verify its state in the local portal's **MeshCore**
tab. Starting a selected MeshCore Session in Cloud then manages telemetry
collection through that Receiver.

The MeshCore tab shows the configured and connected device, connection state,
Session handshake state, and request-side route evidence. Cloud's Receiver
panel provides Scan, Connect/Pair, Reconnect, Forget, Release, and Resume
actions; the Receiver remains the authority for local BlueZ work. Releasing
preserves the Bluetooth bond and returns the Companion to another client.
After resume or a Receiver restart, Cloud reaffirms any active Session before
Session-managed collection resumes.

MeshCore support is complete for LoRaMapr 2.1.0. The Receiver provides
heartbeat/status/diagnostics, automatic BLE recovery, a durable normalized
event outbox, and a durable Linux/systemd service path. Valid position-bearing
observations are delivered with provenance for Cloud's unified Session and
latest-observed Device workflows; coverage eligibility remains a separate
Cloud policy.

See [MeshCore Companion support for LoRaMapr 2.1.0](./docs/meshcore-companion.md) for
the BLE configuration, pairing flow, status meanings, and data limitations.

## Supported Install Path

LoRaMapr Receiver uses one first-class Linux/Pi install path:

1. Existing Debian-family Linux / Raspberry Pi OS Lite
   - Install `loramapr-receiver` from the signed APT repository.
   - APT origin: `https://downloads.loramapr.com/apt/stable` (currently served via GitHub Pages).
   - Guide: [Linux/Pi Existing-OS Path](./docs/linux-pi-distribution.md)
   - Quick install:
     - `curl -fsSL https://raw.githubusercontent.com/kpax2049/loramapr-receiver/main/packaging/linux/scripts/bootstrap-apt.sh | sudo bash`
   - Local/self-hosted cloud override:
     - `curl -fsSL https://raw.githubusercontent.com/kpax2049/loramapr-receiver/main/packaging/linux/scripts/bootstrap-apt.sh | sudo bash -s -- --cloud-base-url http://<cloud-host-or-ip>:3001`
   - Safe upgrade on existing host:
     - `sudo /usr/share/loramapr/scripts/update-receiver.sh`

Raspberry Pi appliance image flow is currently deprecated/paused.

Advanced fallback:

- Manual systemd-layout tarball install (for constrained environments only).

## Pairing and First Run

After install, the receiver enters pairing-ready mode and waits for a pairing
code from LoRaMapr Cloud.

1. Open the local portal:
   - Linux/Pi OS host: `http://loramapr-receiver.local:8080`
   - fallback: `http://<device-lan-ip>:8080`
2. Go to **Pairing**.
3. Paste the pairing code from LoRaMapr Cloud.
4. Wait until the portal shows paired/ready state.
5. Confirm the selected protocol connection and forwarding on **Progress** or,
   for MeshCore, on **MeshCore**.
6. If setup is blocked, check **Setup Root Cause** on Progress/Troubleshooting
   (or `GET /api/ops` -> `setup_issues`) for concrete next steps.

Portal reference: [Embedded Local Setup Portal](./docs/local-portal.md)

For households/teams with multiple receivers, see
[Multi-Receiver Identity and Guidance](./docs/multi-receiver-identity.md).

Optional automation module:

- [Embedded Home Auto Session (Milestone 4)](./docs/home-auto-session.md)
- [MeshCore Companion setup and operation](./docs/meshcore-companion.md)

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
- [APT Hosting via GitHub Pages](./docs/apt-pages-hosting.md)
- [Reviewer Smoke Test Guide](./docs/reviewer-smoke-test.md)
- [Release Notes](./docs/release-notes.md)

## Maintainer/Developer Notes

Repository layout:

- `cmd/loramapr-receiverd`: runtime entrypoint
- `internal/`: runtime subsystems (config/state/cloud/portal/adapter/runtime)
- `docs/`: product and operational documentation
- `packaging/`: release, package, distribution, and deprecated image scaffolding

Build from source:

```bash
go build -o bin/loramapr-receiverd ./cmd/loramapr-receiverd
```
