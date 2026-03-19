# Reviewer Smoke Test Guide (v2.16.0 One-Click Hardening)

This guide validates first-run behavior on the supported Debian-family
Linux/Raspberry Pi OS Lite path.

## 1. Build/Test Baseline

```bash
GOCACHE=/tmp/go-build-cache GOTMPDIR=/tmp go test ./...
GOCACHE=/tmp/go-build-cache GOTMPDIR=/tmp go build -o /tmp/loramapr-receiverd ./cmd/loramapr-receiverd
```

## 2. Fresh Pi/Linux Install Path

On a clean Raspberry Pi OS Lite (or Debian-family) host:

```bash
curl -fsSL https://raw.githubusercontent.com/kpax2049/loramapr-receiver/main/packaging/linux/scripts/bootstrap-apt.sh | sudo bash
```

Validate:

1. `systemctl status loramapr-receiverd` is active/running.
2. `/etc/loramapr/receiver.json` contains:
   - `runtime.profile = linux-service`
   - `paths.state_file = /var/lib/loramapr/receiver-state.json`
   - `portal.bind_address = 0.0.0.0:8080`
3. service user and permissions are correct:
   - `id loramapr` includes `dialout`
   - `/var/lib/loramapr` and `/var/log/loramapr` owned by `loramapr:loramapr`

## 3. Portal Reachability and Pairing Readiness

1. Open:
   - `http://loramapr-receiver.local:8080` (preferred)
   - fallback `http://<pi-lan-ip>:8080`
2. Confirm Welcome/Progress show pairing-ready state.
3. For local/self-hosted cloud, validate non-manual config path:
   - rerun bootstrap with `--cloud-base-url`, or
   - `sudo /usr/bin/loramapr-receiverd configure-cloud -config /etc/loramapr/receiver.json -base-url http://<cloud-host-or-ip>:3001`

## 4. Native Meshtastic USB Path

1. Attach Meshtastic node over USB.
2. Confirm Progress transitions:
   - `meshtastic` from `not_present`/`detected`/`connecting` to `connected`
3. Confirm no JSON bridge is required in supported path.
4. Confirm packet forwarding begins once node traffic exists.

## 5. Setup Root-Cause UX

When setup is blocked, verify:

1. Progress page shows **Setup Root Cause** section.
2. Troubleshooting page shows concrete setup issues and next steps.
3. `GET /api/ops` includes `setup_issues[]`.
4. `support-snapshot` includes `setup.issues`.

Expected issue classes:

- portal bind/config issues (`portal_bind_localhost`, `portal_unavailable`)
- cloud endpoint issues (`cloud_base_url_*`, `cloud_unreachable`)
- USB/node issues (`usb_device_not_detected`, `usb_detected_node_not_ready`,
  `usb_protocol_unusable`, `usb_serial_permission_denied`)
- forwarding readiness (`packets_not_ingesting`)

## 6. Ingest Path

With paired receiver and connected node:

1. Verify `forwarding_activity` moves to `ok` when acked packets appear.
2. Verify `Last Packet Ack` updates in Progress.
3. Confirm cloud-facing receiver status remains healthy.

## 7. Support Data Export

```bash
loramapr-receiverd doctor -config /etc/loramapr/receiver.json
loramapr-receiverd support-snapshot -config /etc/loramapr/receiver.json -out /tmp/receiver-support.json
```

Verify snapshot includes:

- attention/failure/operational summary
- `setup.issues`
- no secret leakage (pairing code, activation token, ingest secret, raw share URL)

