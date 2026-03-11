# Signed APT Repository (Debian-family)

This directory contains the signed APT publication path for Linux/Pi existing-OS
installs.

## Scope

- Debian-family repo layout (`pool/`, `dists/`)
- architectures: `amd64`, `arm64`, `armhf`
- signed `Release` metadata (`InRelease` and `Release.gpg`) when signing is enabled
- maintainer-friendly publish/verify scripts

## Publish

From repository root, after building release artifacts:

```bash
packaging/release/build-artifacts.sh <version> <channel>
GPG_KEY_ID=<key-id> SIGNING_MODE=required \
  packaging/distribution/apt/publish-apt.sh <version> <channel>
```

Output tree:

- `dist/published/apt/<channel>/pool/main/l/loramapr-receiver/*.deb`
- `dist/published/apt/<channel>/dists/<suite>/main/binary-*/Packages{,.gz}`
- `dist/published/apt/<channel>/dists/<suite>/Release`
- `dist/published/apt/<channel>/dists/<suite>/InRelease` (when signed)
- `dist/published/apt/<channel>/dists/<suite>/Release.gpg` (when signed)
- `dist/published/apt/<channel>/loramapr-archive-keyring.{asc,gpg}` (when signed)
- `dist/published/apt/<channel>/apt-summary.json`

## Verify

```bash
packaging/distribution/apt/verify-apt.sh <channel>
```

To require signatures during verification:

```bash
SIGNING_REQUIRED=1 packaging/distribution/apt/verify-apt.sh <channel>
```

## End-user install (expected)

```bash
sudo install -d -m 0755 /usr/share/keyrings
curl -fsSL https://downloads.loramapr.com/apt/stable/loramapr-archive-keyring.asc \
  | gpg --dearmor \
  | sudo tee /usr/share/keyrings/loramapr-archive-keyring.gpg >/dev/null
echo "deb [signed-by=/usr/share/keyrings/loramapr-archive-keyring.gpg] https://downloads.loramapr.com/apt/stable stable main" \
  | sudo tee /etc/apt/sources.list.d/loramapr-receiver.list
sudo apt-get update
sudo apt-get install -y loramapr-receiver
```

## Notes

- Signing keys are not stored in repository files.
- If signing is optional/disabled, maintainers can still stage and inspect repo
  metadata before final signed publication.
