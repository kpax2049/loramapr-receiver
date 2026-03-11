# APT Repository Skeleton (Future)

LoRaMapr Receiver currently publishes signed tarball-based artifacts and metadata
for Linux/Pi distribution.

This folder is reserved for a future native APT repository path once `.deb`
artifacts are promoted to first-class release outputs.

Planned future additions:

- `Packages`/`Packages.gz` generation per architecture
- `Release`/`InRelease` metadata signing
- repository publish automation for stable/beta channels

Current Linux/Pi signed publication flow lives in `packaging/distribution/`.
