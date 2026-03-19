# Debian Packaging

This directory provides the native Debian-family package build path for
`loramapr-receiver`.

## Build Command

The release pipeline calls:

```bash
packaging/debian/build-deb.sh <version> <arch-label> <binary-path> <output-dir>
```

Architecture labels map as:

- `amd64` -> `amd64`
- `arm64` -> `arm64`
- `armv7` -> `armhf`

Release tags like `v2.1.0` are automatically normalized for Debian control
metadata (`2.1.0`) while artifact filenames keep the original receiver version
string.

## Package Contents

- `/usr/bin/loramapr-receiverd`
- `/usr/share/loramapr/scripts/update-receiver.sh`
- `/lib/systemd/system/loramapr-receiverd.service`
- `/etc/loramapr/receiver.json` (dpkg conffile)
- `/var/lib/loramapr`
- `/var/log/loramapr`

`/etc/loramapr/receiver.json` is sourced from:

- `packaging/linux/receiver.linux-package.json`

Maintainer scripts in `scripts/` handle service lifecycle and runtime directory
setup for install/upgrade/remove/purge flows, including:

- service account normalization (`loramapr`)
- serial-access hardening (`dialout` membership)
- runtime path ownership/permission normalization
- bounded service transition timeouts for install/upgrade lifecycle operations

## Validation

Use:

```bash
packaging/debian/validate-deb.sh <deb-file>
packaging/debian/validate-lifecycle.sh <deb-file>
```

This checks package metadata and expected content paths.

## Reference

- `docs/ga-v2.1.0-linux-pi-existing-os.md`
- `docs/linux-pi-distribution.md`
