#!/usr/bin/env bash
# Go TUI preview installer. Does not modify Remnawave components.
set -Eeuo pipefail
umask 077
fail() { printf 'remna: %s\n' "$*" >&2; exit 1; }
trap 'printf "remna: установка прервана (строка %s).\n" "$LINENO" >&2' ERR
[[ $(uname -s) == Linux && $EUID == 0 ]] || fail 'Нужен Linux и запуск от root (sudo bash).'
[[ -r /etc/os-release ]] || fail 'Не найден /etc/os-release.'
# shellcheck disable=SC1091
. /etc/os-release
case ${ID:-}:${VERSION_ID:-} in
    ubuntu:22.04|ubuntu:24.04|debian:12|debian:13) ;;
    *) fail 'Поддерживаются Ubuntu 22.04/24.04 и Debian 12/13.' ;;
esac
case $(uname -m) in x86_64) arch=amd64;; aarch64|arm64) arch=arm64;; *) fail 'Поддерживаются amd64 и arm64.';; esac
ref=main
source_dir=''
while (($#)); do
    case $1 in
        --ref) (($# >= 2)) || fail 'Укажите --ref BRANCH.'; ref=$2; shift 2;;
        --source) (($# >= 2)) || fail 'Укажите --source DIRECTORY.'; source_dir=$2; shift 2;;
        *) fail "Неизвестный аргумент: $1";;
    esac
done
[[ $ref =~ ^[a-zA-Z0-9][a-zA-Z0-9._/-]*$ && $ref != *..* ]] || fail 'Некорректная Git-ссылка.'
for utility in apt-get dpkg dpkg-query mktemp install mv ln uname awk grep mkdir rm; do
    command -v "$utility" >/dev/null || fail "Не найдена системная утилита $utility."
done
missing=()
for pair in curl:curl jq:jq tar:tar gzip:gzip sha256sum:coreutils flock:util-linux; do
    command -v "${pair%%:*}" >/dev/null || missing+=("${pair#*:}")
done
if ! dpkg-query -W -f='${Status}' ca-certificates 2>/dev/null | grep -q 'install ok installed'; then missing+=(ca-certificates); fi
if ((${#missing[@]})); then
    apt-get update
    DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends "${missing[@]}"
fi
install -d -m 0755 /usr/local/lib/remnamanager /usr/local/bin
exec 9>/usr/local/lib/remnamanager/install.lock
flock -n 9 || fail 'Другой установщик уже работает.'
temporary=$(mktemp -d /usr/local/lib/remnamanager/install.XXXXXXXX)
trap 'rm -rf -- "$temporary"' EXIT
trap 'exit 130' INT
trap 'exit 143' TERM HUP
download() {
    curl --fail --silent --show-error --location --proto '=https' --proto-redir '=https' \
        --connect-timeout 15 --max-time 300 --retry 2 "$1" -o "$2"
}
if [[ -z $source_dir ]]; then
    printf 'Загрузка исходников ConsolePro/RemnaManager (%s)…\n' "$ref"
    download "https://codeload.github.com/ConsolePro/RemnaManager/tar.gz/$ref" "$temporary/source.tar.gz"
    mkdir "$temporary/source"
    tar -xzf "$temporary/source.tar.gz" --strip-components=1 --no-same-owner -C "$temporary/source"
    source_dir=$temporary/source
else
    source_dir=$(cd -- "$source_dir" && pwd)
fi
[[ -f $source_dir/go.mod && -f $source_dir/cmd/remna/main.go ]] || fail 'В этой Git-ссылке нет Go-версии. Для прототипа укажите --ref feature/go-tui.'
required=$(awk '$1=="go" {print $2; exit}' "$source_dir/go.mod")
[[ $required =~ ^[0-9]+\.[0-9]+(\.[0-9]+)?$ ]] || fail 'Некорректная версия Go в go.mod.'
go_bin=$(command -v go || true)
existing=''
if [[ -n $go_bin ]]; then existing=$("$go_bin" version | awk '{sub(/^go/,"",$3); print $3}'); fi
if [[ -z $existing ]] || ! dpkg --compare-versions "$existing" ge "$required"; then
    printf 'Загрузка поддерживаемого Go SDK…\n'
    download 'https://go.dev/dl/?mode=json' "$temporary/go.json"
    sdk=$(jq -er '[.[] | select(.stable)][0].version' "$temporary/go.json")
    dpkg --compare-versions "${sdk#go}" ge "$required" || fail 'Требуется более новая версия Go.'
    filename=$sdk.linux-$arch.tar.gz
    digest=$(jq -er --arg name "$filename" '.[] | .files[] | select(.filename==$name) | .sha256' "$temporary/go.json")
    [[ $digest =~ ^[0-9a-f]{64}$ ]] || fail 'Не найден SHA256 Go SDK.'
    download "https://go.dev/dl/$filename" "$temporary/go.tar.gz"
    printf '%s  %s\n' "$digest" "$temporary/go.tar.gz" | sha256sum -c -
    tar -xzf "$temporary/go.tar.gz" --no-same-owner -C "$temporary"
    go_bin=$temporary/go/bin/go
fi
printf 'Сборка Go TUI preview…\n'
(
    cd -- "$source_dir"
    export CGO_ENABLED=0 GOTOOLCHAIN=local
    export GOPATH=$temporary/gopath GOCACHE=$temporary/gocache
    "$go_bin" mod download
    "$go_bin" mod verify
    "$go_bin" build -mod=readonly -trimpath -o "$temporary/remna" ./cmd/remna
)
"$temporary/remna" --version
install -m 0755 "$temporary/remna" /usr/local/bin/.remna.new
mv -f -- /usr/local/bin/.remna.new /usr/local/bin/remna
ln -sfn remna /usr/local/bin/remna_manager
ln -sfn remna /usr/local/bin/re
install -d -m 0700 /etc/remnamanager
printf '\nGo TUI preview установлен. Запуск: sudo remna\nНастройки: /etc/remnamanager/config.json (существующий файл сохранён).\n'
