# LoRaMapr Receiver

LoRaMapr Receiver is the local runtime that connects a Meshtastic node to
LoRaMapr Cloud.

It runs as a background service (`loramapr-receiverd`), exposes a local setup
portal, and forwards packets to the existing cloud ingest path after pairing.

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
5. Confirm Meshtastic node connection and forwarding on **Progress**.
6. If setup is blocked, check **Setup Root Cause** on Progress/Troubleshooting
   (or `GET /api/ops` -> `setup_issues`) for concrete next steps.

Portal reference: [Embedded Local Setup Portal](./docs/local-portal.md)

For households/teams with multiple receivers, see
[Multi-Receiver Identity and Guidance](./docs/multi-receiver-identity.md).

Optional automation module:

- [Embedded Home Auto Session (Milestone 4)](./docs/home-auto-session.md)

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
