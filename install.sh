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
printf '  […] Скачиваю последнюю опубликованную версию RemnaManager\n' >&2
debug=0
for argument in "$@"; do [[ $argument != --debug ]] || debug=1; done
download_release() {
    curl --fail --silent --show-error --location --proto '=https' --proto-redir '=https' --connect-timeout 15 --max-time 180 --max-filesize 16777216 \
        https://github.com/OverlayNode/RemnaManager/releases/latest/download/remnamanager.sh -o "$installer"
}
if ((debug)); then download_release
elif ! download_release >"$temporary/download.log" 2>&1; then
    printf '  [✗] Не удалось скачать Release. Повторите запуск с --debug для подробностей.\n' >&2
    exit 1
fi
if ((EUID == 0)); then
    bash "$installer" "$@"
else
    command -v sudo >/dev/null || { printf 'Выполните установку от root или установите sudo.\n' >&2; exit 1; }
    sudo bash "$installer" "$@"
fi
