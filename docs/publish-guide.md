# Receiver Publish Guide

This guide is for maintainers publishing LoRaMapr Receiver artifacts.

For user install flows, use:

- [Raspberry Pi Appliance Path](./raspberry-pi-appliance.md)
- [Linux/Pi Existing-OS Install Path](./linux-pi-distribution.md)

## 1. Build Release Artifacts

```bash
packaging/release/build-artifacts.sh <version> <channel>
```

For Pi image releases:

```bash
PI_GEN_DIR=/path/to/pi-gen ENABLE_PI_IMAGE=1 \
  packaging/release/build-artifacts.sh <version> <channel>
```

Outputs in `dist/<version>/artifacts/` include release artifacts, checksums,
manifest fragment, and release metadata.

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

## 3. Verify Publication

```bash
packaging/distribution/verify.sh <version> <channel>
```

If Pi image is required in this release:

```bash
PI_IMAGE_REQUIRED=1 packaging/distribution/verify.sh <version> <channel>
```

## 4. Cloud Artifact Mapping

Cloud should consume:

- `cloud-manifest.fragment.json`
- URL pattern `receiver/<channel>/<version>/<artifact-file>`

Preferred kinds for onboarding:

- `appliance_image` for Raspberry Pi appliance path
- `deb_package` for existing-OS package path

## 5. Diagnostics Sanity Check

Before final release announcement:

```bash
loramapr-receiverd doctor -config /etc/loramapr/receiver.json
loramapr-receiverd support-snapshot -config /etc/loramapr/receiver.json -out /tmp/receiver-support.json
```

## Related References

- [Release Artifact Mapping](./release-artifacts.md)
- [Support and Troubleshooting Workflow](./support-operations-workflow.md)
- [Version, Channel, and Upgrade Safety](./version-channel-upgrades.md)
- `packaging/distribution/apt/README.md`
