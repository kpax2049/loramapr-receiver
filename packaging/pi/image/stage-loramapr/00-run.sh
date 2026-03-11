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

echo "loramapr-receiver" > "${ROOTFS_DIR}/etc/hostname"
if grep -qE '^127\.0\.1\.1\s+' "${ROOTFS_DIR}/etc/hosts"; then
  sed -i 's/^127\.0\.1\.1.*/127.0.1.1\tloramapr-receiver/' "${ROOTFS_DIR}/etc/hosts"
else
  echo -e "127.0.1.1\tloramapr-receiver" >> "${ROOTFS_DIR}/etc/hosts"
fi

install -d "${ROOTFS_DIR}/etc/systemd/system/multi-user.target.wants"
ln -sf /etc/systemd/system/loramapr-receiverd.service \
  "${ROOTFS_DIR}/etc/systemd/system/multi-user.target.wants/loramapr-receiverd.service"
ln -sf /lib/systemd/system/avahi-daemon.service \
  "${ROOTFS_DIR}/etc/systemd/system/multi-user.target.wants/avahi-daemon.service"

install -d "${ROOTFS_DIR}/etc/avahi/services"
cat > "${ROOTFS_DIR}/etc/avahi/services/loramapr-receiver-portal.service" <<'SERVICE'
<?xml version="1.0" standalone='no'?>
<!DOCTYPE service-group SYSTEM "avahi-service.dtd">
<service-group>
  <name replace-wildcards="yes">LoRaMapr Receiver (%h)</name>
  <service>
    <type>_http._tcp</type>
    <port>8080</port>
    <txt-record>path=/</txt-record>
  </service>
</service-group>
SERVICE
