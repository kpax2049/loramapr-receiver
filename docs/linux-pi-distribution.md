# Linux/Pi Existing-OS Install Path

This is the official LoRaMapr Receiver install path for Debian-family Linux and
Raspberry Pi OS.

Recommended Raspberry Pi host path:

- flash official Raspberry Pi OS Lite
- preconfigure Wi-Fi/hostname in Raspberry Pi Imager
- install `loramapr-receiver` package on first boot

Receiver appliance image flow is currently deprecated/paused.

APT host note:

- `downloads.loramapr.com` is currently served via GitHub Pages static hosting.
- Public APT URL remains stable and should not change for clients.

## Supported Systems

- Debian-family OS:
  - Debian
  - Ubuntu-family
  - Raspberry Pi OS
- Architectures:
  - `amd64`
  - `arm64`
  - `armv7` (`armhf` package architecture)

## Quick Install (Canonical)

Run on the target Linux/Pi host:

```bash
curl -fsSL https://raw.githubusercontent.com/kpax2049/loramapr-receiver/main/packaging/linux/scripts/bootstrap-apt.sh | sudo bash
```

Optional beta channel install:

```bash
curl -fsSL https://raw.githubusercontent.com/kpax2049/loramapr-receiver/main/packaging/linux/scripts/bootstrap-apt.sh | sudo bash -s -- --channel beta
```

The bootstrap script:

- installs `curl`/`gnupg` prerequisites if missing
- installs LoRaMapr APT keyring and source
- installs `loramapr-receiver`
- enables/starts `loramapr-receiverd`

If bootstrap fails early, verify host reachability:

```bash
curl -fsSL https://downloads.loramapr.com/apt/stable/loramapr-archive-keyring.asc | head
curl -fsSL https://downloads.loramapr.com/apt/stable/dists/stable/InRelease | head
```

## After Install

1. Confirm service is running: `systemctl status loramapr-receiverd`
2. Open local portal:
   - `http://loramapr-receiver.local:8080` (if mDNS available)
   - or `http://<host-lan-ip>:8080`
3. Enter pairing code from LoRaMapr Cloud.

Home Auto Session inclusion:

- Feature is built into the installed receiver package.
- Default is off (`enabled=false`, `mode=off`).
- Enable/configure from local portal: `/home-auto-session`.

## Manual APT Setup (Fallback)

Use this when you do not want to use the bootstrap helper.

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

## Manual `.deb` Install (Fallback)

Use this only when APT repository access is not possible.

`amd64` example:

```bash
VERSION=v2.15.0
CHANNEL=stable
BASE=https://downloads.loramapr.com/receiver/${CHANNEL}/${VERSION}

curl -fsSLO "${BASE}/loramapr-receiver_${VERSION}_linux_amd64.deb"
curl -fsSLO "${BASE}/SHA256SUMS"
sha256sum -c SHA256SUMS --ignore-missing

sudo apt-get update
sudo apt-get install -y ./loramapr-receiver_${VERSION}_linux_amd64.deb
```

`arm64` example (Raspberry Pi OS 64-bit):

```bash
VERSION=v2.15.0
CHANNEL=stable
BASE=https://downloads.loramapr.com/receiver/${CHANNEL}/${VERSION}

curl -fsSLO "${BASE}/loramapr-receiver_${VERSION}_linux_arm64.deb"
curl -fsSLO "${BASE}/SHA256SUMS"
sha256sum -c SHA256SUMS --ignore-missing

sudo apt-get update
sudo apt-get install -y ./loramapr-receiver_${VERSION}_linux_arm64.deb
```

## Advanced Fallback: Systemd Layout Tarball

If package install is not possible, manual systemd layout archives are still
published:

- `loramapr-receiver_<version>_linux_<arch>_systemd.tar.gz`

```bash
sudo tar -xzf "loramapr-receiver_${VERSION}_linux_amd64_systemd.tar.gz" -C /
sudo systemctl daemon-reload
sudo systemctl enable --now loramapr-receiverd
```

## Install/Upgrade/Remove Behavior

- `apt upgrade`: keeps config/state and restarts service safely
- `apt remove`: removes package and stops service, keeps config/state
- `apt purge`: removes package plus config/state (full local reset)

Detailed lifecycle policy:

- [Debian-family Package Lifecycle Behavior](./linux-package-lifecycle.md)

## Published Artifacts and Integrity Files

Per release version/channel (`receiver/<channel>/<version>/`), published outputs
include:

- `loramapr-receiver_<version>_linux_amd64.deb`
- `loramapr-receiver_<version>_linux_arm64.deb`
- `loramapr-receiver_<version>_linux_armv7.deb`
- `SHA256SUMS`
- optional detached signatures (`*.asc`) when signing is enabled
- `cloud-manifest.fragment.json`
- `release-metadata.json`

## Maintainer Publication References

Maintainer publish/verify flow:

- `packaging/release/build-artifacts.sh <version> <channel>`
- `packaging/distribution/publish.sh <version> <channel>`
- `packaging/distribution/verify.sh <version> <channel>`
- `packaging/distribution/pages/verify-pages-tree.sh <version> <channel>`
- `packaging/distribution/apt/README.md`

Cloud artifact mapping:

- [Release Artifact Mapping](./release-artifacts.md)
- [APT Pages Hosting (Temporary)](./apt-pages-hosting.md)
