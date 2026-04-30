#!/bin/bash -e
# Chroot-side substage. Configures the system to hand wlan0 / ap0
# entirely to knotd: stock services that would fight for those
# interfaces are masked, knotd is enabled.

# Default Raspberry Pi OS Lite uses NetworkManager (Bookworm) — disable
# it so it does not race knotd for wlan0. eth0 is left alone for
# future Ethernet WAN support; on Zero 2W there is no eth0 anyway.
systemctl mask NetworkManager.service          2>/dev/null || true
systemctl disable NetworkManager.service       2>/dev/null || true
systemctl mask NetworkManager-wait-online.service 2>/dev/null || true
systemctl disable wpa_supplicant.service       2>/dev/null || true
systemctl mask hostapd.service                 2>/dev/null || true
systemctl mask dnsmasq.service                 2>/dev/null || true
systemctl disable dhcpcd.service               2>/dev/null || true
systemctl mask dhcpcd.service                  2>/dev/null || true

# avahi-daemon stays enabled for mDNS resolution of <hostname>.local.

# Enable knotd. After flashing, the device will boot straight into
# the open setup AP and serve the wizard at 192.168.42.1.
systemctl enable knotd.service

# /run/knot is created by knotd itself on every boot, but tmpfiles.d
# keeps systemd happy if anything else looks for it earlier.
cat > /etc/tmpfiles.d/knot.conf <<'EOF'
d /run/knot 0755 root root -
EOF

# Default hostname comes from default-config.yaml -> "knot". This
# lets users find the device at knot.local before they pick a name.
echo "knot" > /etc/hostname
sed -i 's/127\.0\.1\.1.*/127.0.1.1\tknot/' /etc/hosts || true
