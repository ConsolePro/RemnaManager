#!/usr/bin/env bash
set -Eeuo pipefail
proxy=$1
arch=$2
# shellcheck disable=SC1091
. /etc/os-release
case "$ID:$VERSION_ID" in debian:12|debian:13|ubuntu:22.04|ubuntu:24.04) ;; *) exit 1;; esac
missing=()
for package in ca-certificates curl gnupg nftables iproute2 openssh-server util-linux; do
    if ! dpkg-query -W -f='${Status}' "$package" 2>/dev/null | grep -q 'install ok installed'; then missing+=("$package"); fi
done
if [[ $proxy == nginx ]]; then packages=(nginx certbot); else packages=(caddy); fi
for package in "${packages[@]}"; do
    if ! dpkg-query -W -f='${Status}' "$package" 2>/dev/null | grep -q 'install ok installed'; then missing+=("$package"); fi
done
if ((${#missing[@]})); then apt-get update; apt-get install -y --no-install-recommends "${missing[@]}"; fi
if ! command -v docker >/dev/null; then
    install -d -m 0755 /etc/apt/keyrings
    curl -fsSL --proto '=https' --proto-redir '=https' --connect-timeout 15 --max-time 60 "https://download.docker.com/linux/$ID/gpg" -o /etc/apt/keyrings/remnamanager-docker.asc
    chmod 0644 /etc/apt/keyrings/remnamanager-docker.asc
    printf 'deb [arch=%s signed-by=/etc/apt/keyrings/remnamanager-docker.asc] https://download.docker.com/linux/%s %s stable\n' "$arch" "$ID" "$VERSION_CODENAME" >/etc/apt/sources.list.d/remnamanager-docker.list
    apt-get update
    apt-get install -y --no-install-recommends docker-ce docker-ce-cli containerd.io docker-compose-plugin
fi
systemctl enable --now docker
docker compose version
# Do not silently upgrade an existing engine or expose old loopback bindings.
version=$(docker version --format '{{.Server.Version}}')
major=${version%%.*}
[[ $major =~ ^[0-9]+$ && $major -ge 28 ]] || { echo 'Docker Engine 28+ is required; upgrade the existing installation explicitly.' >&2; exit 1; }
