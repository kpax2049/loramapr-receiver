# Reviewer Smoke Test Guide (v2.14.0 Pi OS Lite Strategy)

## 1. Build and Baseline Tests

```bash
GOCACHE=/tmp/go-build-cache GOTMPDIR=/tmp make test
GOCACHE=/tmp/go-build-cache GOTMPDIR=/tmp make build
```

## 2. Raspberry Pi OS Lite Install Path (Primary)

Use [Linux/Pi Existing-OS Install Path](./linux-pi-distribution.md):

1. Flash official Raspberry Pi OS Lite (64-bit recommended).
2. Configure Wi-Fi/hostname in Raspberry Pi Imager.
3. Boot Pi and run canonical receiver install command path.
4. Confirm service is running:
   - `systemctl status loramapr-receiverd`
5. Open local portal:
   - `http://loramapr-receiver.local:8080` (preferred)
   - `http://<pi-lan-ip>:8080` (fallback)
6. Confirm pairing page is reachable and pairing-ready.

## 3. Package Lifecycle Sanity

1. Verify upgrade path:
   - `sudo apt-get update && sudo apt-get install -y --only-upgrade loramapr-receiver`
2. Verify remove keeps state/config as documented:
   - `sudo apt remove -y loramapr-receiver`
3. Verify reinstall returns to pairing-ready/service-running state.

## 4. Diagnostics and Local Support

Run:

```bash
loramapr-receiverd doctor -config /etc/loramapr/receiver.json
loramapr-receiverd support-snapshot -config /etc/loramapr/receiver.json -out /tmp/receiver-support.json
```

Verify:

- diagnostics are support-safe (no secrets)
- pairing/cloud/node/forwarding checks are visible and actionable

## 5. Deprecation Surface Check

Confirm public docs/release guidance no longer present Pi appliance image as a
first-class current path and instead point Pi users to Pi OS Lite + package
install.
