#!/usr/bin/env bash
# One-time preparation of a fresh Ubuntu/Debian VPS: Docker, a firewall that
# opens only SSH and the web ports, and unattended security updates.
#
#   curl -fsSL https://raw.githubusercontent.com/<you>/<repo>/main/deploy/install-vps.sh | sudo bash
#   # or, after cloning:
#   sudo bash deploy/install-vps.sh
set -euo pipefail

if [[ $EUID -ne 0 ]]; then
  echo "Jalankan dengan sudo." >&2
  exit 1
fi

export DEBIAN_FRONTEND=noninteractive
apt-get update -y
apt-get install -y ca-certificates curl git ufw unattended-upgrades

# --- Docker (official repository, includes the compose plugin) ---------------
if ! command -v docker >/dev/null 2>&1; then
  install -m 0755 -d /etc/apt/keyrings
  . /etc/os-release
  curl -fsSL "https://download.docker.com/linux/${ID}/gpg" -o /etc/apt/keyrings/docker.asc
  chmod a+r /etc/apt/keyrings/docker.asc
  echo "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.asc] \
https://download.docker.com/linux/${ID} ${VERSION_CODENAME} stable" \
    > /etc/apt/sources.list.d/docker.list
  apt-get update -y
  apt-get install -y docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin
  systemctl enable --now docker
fi

# Let the login user run docker without sudo (takes effect on next login).
if [[ -n "${SUDO_USER:-}" ]]; then
  usermod -aG docker "$SUDO_USER"
fi

# --- Firewall: SSH, HTTP, HTTPS. Nothing else, including 8080 and 3000. -----
ufw default deny incoming
ufw default allow outgoing
ufw allow OpenSSH
ufw allow 80/tcp
ufw allow 443/tcp
ufw allow 443/udp
ufw --force enable

# --- Security patches apply themselves --------------------------------------
dpkg-reconfigure -f noninteractive unattended-upgrades

echo
echo "Selesai. Docker $(docker --version | cut -d' ' -f3) terpasang, firewall aktif (22, 80, 443)."
echo "Keluar dari SSH lalu masuk lagi supaya grup docker berlaku, kemudian jalankan deploy/deploy.sh."
