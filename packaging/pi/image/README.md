# Raspberry Pi Image Build Scaffolding

This directory provides a practical skeleton for building a Raspberry Pi image
that includes LoRaMapr Receiver preinstalled and enabled at boot.

## Prerequisites

1. A release artifact from this repository:
   - `dist/<version>/artifacts/loramapr-receiver_<version>_linux_arm64_systemd.tar.gz`
2. A local `pi-gen` workspace for Raspberry Pi OS image builds.

## Prepare Build

```bash
PI_GEN_DIR=/path/to/pi-gen \
packaging/pi/image/build-image.sh v1.0.0
```

Optional override for artifact path:

```bash
PI_GEN_DIR=/path/to/pi-gen \
LORAMAPR_ARM64_SYSTEMD_TARBALL=/tmp/loramapr-receiver_v1.0.0_linux_arm64_systemd.tar.gz \
packaging/pi/image/build-image.sh v1.0.0
```

## Build Image

After preparation, run from `pi-gen` root:

```bash
./build-docker.sh -c loramapr.config
```

The resulting image under `pi-gen/deploy/` contains the receiver runtime,
appliance config defaults, and service enablement symlink.
