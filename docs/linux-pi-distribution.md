# Linux and Raspberry Pi Distribution Path

This document defines the Linux/Pi Existing-OS distribution path for
LoRaMapr Receiver `v2.1.x`.

## Supported Targets

- Debian-family operating systems only:
  - Debian
  - Ubuntu-family
  - Raspberry Pi OS
- Architectures:
  - `amd64`
  - `arm64`
  - `armv7` (`armhf` package architecture)

## Published Structure

Published artifacts are expected at:

- `https://downloads.loramapr.com/receiver/<channel>/<version>/...`

Per-version outputs include:

- `.deb` packages:
  - `loramapr-receiver_<version>_linux_amd64.deb`
  - `loramapr-receiver_<version>_linux_arm64.deb`
  - `loramapr-receiver_<version>_linux_armv7.deb`
- fallback layout archives:
  - `loramapr-receiver_<version>_linux_amd64_systemd.tar.gz`
  - `loramapr-receiver_<version>_linux_arm64_systemd.tar.gz`
  - `loramapr-receiver_<version>_linux_armv7_systemd.tar.gz`
- checksums and metadata:
  - `SHA256SUMS`
  - `cloud-manifest.fragment.json`
  - `release-metadata.json`

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

## Existing-OS Install Path (Primary)

APT repository installation is the primary GA path. See
`packaging/distribution/apt/README.md` for repository layout and publication.

APT installation example:

```bash
CHANNEL=stable
curl -fsSL "https://downloads.loramapr.com/apt/${CHANNEL}/loramapr-archive-keyring.asc" \
  | gpg --dearmor \
  | sudo tee /usr/share/keyrings/loramapr-archive-keyring.gpg >/dev/null
echo "deb [signed-by=/usr/share/keyrings/loramapr-archive-keyring.gpg] https://downloads.loramapr.com/apt/${CHANNEL} ${CHANNEL} main" \
  | sudo tee /etc/apt/sources.list.d/loramapr-receiver.list >/dev/null
sudo apt-get update
sudo apt-get install -y loramapr-receiver
```

Direct package install path (manual fallback):

```bash
VERSION=v2.1.0
CHANNEL=stable
BASE=https://downloads.loramapr.com/receiver/${CHANNEL}/${VERSION}

curl -fsSLO "${BASE}/loramapr-receiver_${VERSION}_linux_amd64.deb"
curl -fsSLO "${BASE}/SHA256SUMS"
sha256sum -c SHA256SUMS --ignore-missing
sudo apt-get update
sudo apt-get install -y ./loramapr-receiver_${VERSION}_linux_amd64.deb
```

On Raspberry Pi OS (64-bit):

```bash
VERSION=v2.1.0
CHANNEL=stable
BASE=https://downloads.loramapr.com/receiver/${CHANNEL}/${VERSION}

curl -fsSLO "${BASE}/loramapr-receiver_${VERSION}_linux_arm64.deb"
curl -fsSLO "${BASE}/SHA256SUMS"
sha256sum -c SHA256SUMS --ignore-missing
sudo apt-get update
sudo apt-get install -y ./loramapr-receiver_${VERSION}_linux_arm64.deb
```

## Fallback / Advanced Path

Systemd layout tarballs remain available for advanced/manual workflows where APT
repository usage is not possible.

```bash
sudo tar -xzf "loramapr-receiver_${VERSION}_linux_amd64_systemd.tar.gz" -C /
sudo systemctl daemon-reload
sudo systemctl enable --now loramapr-receiverd
```

## Package Lifecycle Expectations

- `apt upgrade`: preserves config/state and restarts service safely
- `apt remove`: stops service and keeps config/state
- `apt purge`: removes config/state and resets local receiver data

Detailed lifecycle policy: `docs/linux-package-lifecycle.md`.

## Maintainer Publish Flow

1. Build release artifacts:

```bash
packaging/release/build-artifacts.sh <version> <channel>
```

2. Validate `.deb` structure:

```bash
packaging/debian/validate-deb.sh dist/<version>/artifacts/loramapr-receiver_<version>_linux_amd64.deb
```

3. Stage signed publication tree:

```bash
GPG_KEY_ID=<maintainer-key-id> SIGNING_MODE=required \
  packaging/distribution/publish.sh <version> <channel>
```

4. Validate staged publication:

```bash
packaging/distribution/verify.sh <version> <channel>
```

5. Publish/sync `dist/published/` to hosting.

## Cloud Onboarding Alignment

Cloud should reference `cloud-manifest.fragment.json` and published URLs under
`receiver/<channel>/<version>/`.

Use Raspberry Pi host-choice entries against `platform=raspberry_pi` entries
from the manifest fragment. For existing-OS users, prefer `kind=deb_package`.
