# Receiver Publish Guide (v2.2.0 Raspberry Pi Appliance GA)

This guide ties together artifact generation, signed Linux/Pi publication,
APT repository publication, diagnostics support workflow, and version/channel
reporting.

## 1. Build Release Artifacts

```bash
packaging/release/build-artifacts.sh <version> <channel>
```

For Pi image releases:

```bash
PI_GEN_DIR=/path/to/pi-gen ENABLE_PI_IMAGE=1 \
  packaging/release/build-artifacts.sh <version> <channel>
```

Outputs in `dist/<version>/artifacts/` include:

- platform archives
- Linux/Pi `.deb` packages
- Linux/Pi systemd layout archives
- optional Pi appliance image (`ENABLE_PI_IMAGE=1`)
- `SHA256SUMS`
- `cloud-manifest.fragment.json`
- `release-metadata.json`

## 2. Publish Signed Linux/Pi Distribution

```bash
GPG_KEY_ID=<maintainer-key-id> SIGNING_MODE=required \
  packaging/distribution/publish.sh <version> <channel>
```

Staged publication tree:

- `dist/published/receiver/<channel>/<version>/...`
- `dist/published/receiver/<channel>/channel-index.json`
- `dist/published/apt/<channel>/pool/...`
- `dist/published/apt/<channel>/dists/<suite>/...`

When signing is enabled:

- static artifact detached `*.asc` signatures are generated
- APT metadata is signed (`InRelease`, `Release.gpg`)
- repository public key exports are generated (`loramapr-archive-keyring.asc/.gpg`)

## 3. Verify Publication

```bash
packaging/distribution/verify.sh <version> <channel>
```

This checks static file integrity and APT repository metadata/signature
structure.

If Pi image is part of the release:

```bash
PI_IMAGE_REQUIRED=1 packaging/distribution/verify.sh <version> <channel>
```

## 4. Cloud Onboarding Mapping

Cloud should consume:

- `cloud-manifest.fragment.json`
- published URL pattern `receiver/<channel>/<version>/<artifact-file>`

Pi onboarding should use `platform=raspberry_pi` entries from the manifest and
prefer:

- `kind=appliance_image` for flash-image appliance path
- `kind=deb_package` for existing-OS package path

## 5. Runtime Diagnostics and Support

First-run failures are surfaced by taxonomy codes in local portal and CLI.

Support capture:

```bash
loramapr-receiverd doctor -config /etc/loramapr/receiver.json
loramapr-receiverd support-snapshot -config /etc/loramapr/receiver.json -out /tmp/receiver-support.json
```

Support snapshot is redacted and omits secrets.

## 6. Version/Channel Reporting

Release builds stamp binaries with build metadata (`version`, `channel`,
`commit`, optional `build_date`, `build_id`) using release `ldflags`.
These values surface in:

- `/api/status`
- heartbeat metadata
- portal advanced details
- `doctor`, `status`, and `support-snapshot` outputs

## Related Docs

- `docs/release-artifacts.md`
- `docs/linux-pi-distribution.md`
- `packaging/distribution/apt/README.md`
- `docs/diagnostics.md`
- `docs/version-channel-upgrades.md`
