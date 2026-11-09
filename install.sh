#!/usr/bin/env bash
# Supports bash <(curl -fsSL .../install.sh), keeping stdin free for the menu.
set -Eeuo pipefail
[[ $(uname -s) == Linux ]] || { printf 'Нужен Linux (Debian/Ubuntu).\n' >&2; exit 1; }
command -v curl >/dev/null || { printf 'Установите curl: apt-get update && apt-get install -y ca-certificates curl\n' >&2; exit 1; }
umask 077
temporary=$(mktemp -d "${TMPDIR:-/tmp}/remnamanager-install.XXXXXXXX")
trap 'rm -rf -- "$temporary"' EXIT
trap 'exit 130' INT
trap 'exit 143' HUP TERM
installer=$temporary/remnamanager.sh
curl --fail --silent --show-error --location --proto '=https' --proto-redir '=https' --connect-timeout 15 --max-time 180 --max-filesize 16777216 \
    https://raw.githubusercontent.com/OverlayNode/RemnaManager/refs/heads/main/dist/remnamanager.sh -o "$installer"
if ((EUID == 0)); then
    bash "$installer" "$@"
else
    command -v sudo >/dev/null || { printf 'Выполните установку от root или установите sudo.\n' >&2; exit 1; }
    sudo bash "$installer" "$@"
fi
