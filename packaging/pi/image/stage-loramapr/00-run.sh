#!/bin/bash -e

if [[ ! -f files/loramapr-receiver_systemd.tar.gz ]]; then
  echo "missing files/loramapr-receiver_systemd.tar.gz" >&2
  exit 1
fi

if [[ ! -f files/receiver.appliance.json ]]; then
  echo "missing files/receiver.appliance.json" >&2
  exit 1
fi

install -d "${ROOTFS_DIR}/opt/loramapr"
install -m 0644 files/loramapr-receiver_systemd.tar.gz "${ROOTFS_DIR}/opt/loramapr/loramapr-receiver_systemd.tar.gz"
tar -xzf files/loramapr-receiver_systemd.tar.gz -C "${ROOTFS_DIR}"

install -d "${ROOTFS_DIR}/etc/loramapr"
install -m 0600 files/receiver.appliance.json "${ROOTFS_DIR}/etc/loramapr/receiver.json"

install -d "${ROOTFS_DIR}/etc/systemd/system/multi-user.target.wants"
ln -sf /etc/systemd/system/loramapr-receiverd.service \
  "${ROOTFS_DIR}/etc/systemd/system/multi-user.target.wants/loramapr-receiverd.service"
