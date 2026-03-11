# Linux and Raspberry Pi Distribution Path

This document describes the Linux/Pi-first publish and install story for
LoRaMapr Receiver `v1.1.x`.

## Published Structure

Published artifacts are expected at:

- `https://downloads.loramapr.com/receiver/<channel>/<version>/...`

Examples:

- `.../loramapr-receiver_v1.1.0_linux_amd64_systemd.tar.gz`
- `.../loramapr-receiver_v1.1.0_linux_arm64_systemd.tar.gz`
- `.../SHA256SUMS`
- `.../cloud-manifest.fragment.json`

Channel metadata:

- `https://downloads.loramapr.com/receiver/<channel>/channel-index.json`

## Signature and Integrity Files

Per-version publication includes:

- `SHA256SUMS`
- `SHA256SUMS.asc` (when signed)
- `cloud-manifest.fragment.json`
- `cloud-manifest.fragment.json.asc` (when signed)
- `release-metadata.json`
- `release-metadata.json.asc` (when signed)

Channel index may also include detached signature:

- `channel-index.json.asc`

## Linux Install Path (systemd layout archive)

1. Download Linux systemd archive plus checksums:

```bash
VERSION=v1.1.0
CHANNEL=stable
BASE=https://downloads.loramapr.com/receiver/${CHANNEL}/${VERSION}

curl -fsSLO "${BASE}/loramapr-receiver_${VERSION}_linux_amd64_systemd.tar.gz"
curl -fsSLO "${BASE}/SHA256SUMS"
```

2. Verify checksum:

```bash
sha256sum -c SHA256SUMS --ignore-missing
```

3. Extract to root filesystem and start service:

```bash
sudo tar -xzf "loramapr-receiver_${VERSION}_linux_amd64_systemd.tar.gz" -C /
sudo systemctl daemon-reload
sudo systemctl enable --now loramapr-receiverd
```

## Raspberry Pi Install Path

Preferred artifact:

- `loramapr-receiver_<version>_linux_arm64_systemd.tar.gz`

On Raspberry Pi OS:

```bash
VERSION=v1.1.0
CHANNEL=stable
BASE=https://downloads.loramapr.com/receiver/${CHANNEL}/${VERSION}

curl -fsSLO "${BASE}/loramapr-receiver_${VERSION}_linux_arm64_systemd.tar.gz"
curl -fsSLO "${BASE}/SHA256SUMS"
sha256sum -c SHA256SUMS --ignore-missing
sudo tar -xzf "loramapr-receiver_${VERSION}_linux_arm64_systemd.tar.gz" -C /
sudo systemctl daemon-reload
sudo systemctl enable --now loramapr-receiverd
```

For image-based novice flow, use `docs/raspberry-pi-appliance.md`.

## Maintainer Publish Flow

1. Build release artifacts:

```bash
packaging/release/build-artifacts.sh <version> <channel>
```

2. Stage signed publication tree:

```bash
GPG_KEY_ID=<maintainer-key-id> SIGNING_MODE=required \
  packaging/distribution/publish.sh <version> <channel>
```

3. Validate staged publication:

```bash
packaging/distribution/verify.sh <version> <channel>
```

4. Upload `dist/published/` to release hosting.

## Cloud Onboarding Alignment

Cloud should reference `cloud-manifest.fragment.json` and published URLs under
`receiver/<channel>/<version>/`.

Use Raspberry Pi host-choice entries against `platform=raspberry_pi` entries
from the manifest fragment.
