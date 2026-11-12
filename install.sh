#!/usr/bin/env bash
# RemnaManager Go TUI installer.
# Does not modify Remnawave components.

set -Eeuo pipefail
umask 077

fail() {
    printf '\n\033[31m✗\033[0m remanager: %s\n' "$*" >&2
    exit 1
}

trap 'printf "\nremanager: установка прервана (строка %s).\n" "$LINENO" >&2' ERR

if [[ -t 1 && ${TERM:-dumb} != dumb ]]; then
    UI=true
else
    UI=false
fi

if $UI; then
    GREEN=$'\033[32m'
    RED=$'\033[31m'
    CYAN=$'\033[36m'
    GRAY=$'\033[90m'
    BOLD=$'\033[1m'
    RESET=$'\033[0m'
else
    GREEN=''
    RED=''
    CYAN=''
    GRAY=''
    BOLD=''
    RESET=''
fi

header() {
    printf '\n'
    printf '%s' "$BOLD"
    printf '  REMNA MANAGER\n'
    printf '  Installer\n'
    printf '%s\n\n' "$RESET"
}

# Execute a real command and animate while it runs.
# Command output is retained in a log and shown on failure.
run_step() {
    local title=$1
    shift

    local log pid status=0 frame=0
    local frames=('⠋' '⠙' '⠹' '⠸' '⠼' '⠴' '⠦' '⠧' '⠇' '⠏')

    log=$(mktemp "$temporary/step.XXXXXXXX")

    "$@" >"$log" 2>&1 &
    pid=$!

    if $UI; then
        while kill -0 "$pid" 2>/dev/null; do
            printf '\r\033[2K  %s%s%s %s...' \
                "$CYAN" "${frames[frame]}" "$RESET" "$title"
            frame=$(( (frame + 1) % ${#frames[@]} ))
            sleep 0.12
        done
    else
        printf '  ... %s\n' "$title"
    fi

    wait "$pid" || status=$?

    if ((status == 0)); then
        if $UI; then
            printf '\r\033[2K'
        fi
        printf '  %s✓%s %s\n' "$GREEN" "$RESET" "$title"
    else
        if $UI; then
            printf '\r\033[2K'
        fi
        printf '  %s✗%s %s\n' "$RED" "$RESET" "$title" >&2
        printf '\n%s--- Подробности ошибки ---%s\n' "$GRAY" "$RESET" >&2
        cat "$log" >&2
        printf '%s--------------------------%s\n' "$GRAY" "$RESET" >&2
        rm -f -- "$log"
        return "$status"
    fi

    rm -f -- "$log"
}

section() {
    printf '\n%s%s%s\n' "$BOLD" "$1" "$RESET"
}

download() {
    curl --fail --silent --show-error --location \
        --proto '=https' \
        --proto-redir '=https' \
        --connect-timeout 15 \
        --max-time 300 \
        --retry 2 \
        "$1" -o "$2"
}


header

[[ $(uname -s) == Linux && $EUID == 0 ]] ||
    fail 'Нужен Linux и запуск от root (sudo bash).'

[[ -r /etc/os-release ]] ||
    fail 'Не найден /etc/os-release.'

# shellcheck disable=SC1091
. /etc/os-release

case ${ID:-}:${VERSION_ID:-} in
    ubuntu:22.04|ubuntu:24.04|debian:12|debian:13) ;;
    *) fail 'Поддерживаются Ubuntu 22.04/24.04 и Debian 12/13.' ;;
esac

case $(uname -m) in
    x86_64) arch=amd64 ;;
    aarch64|arm64) arch=arm64 ;;
    *) fail 'Поддерживаются amd64 и arm64.' ;;
esac

ref=main
source_dir=''
source_mode=false
release_version=latest
repository=ConsolePro/RemnaManager

while (($#)); do
    case $1 in
        --ref)
            (($# >= 2)) || fail 'Укажите --ref BRANCH.'
            ref=$2
            source_mode=true
            shift 2
            ;;
        --source)
            (($# >= 2)) || fail 'Укажите --source DIRECTORY.'
            source_dir=$2
            source_mode=true
            shift 2
            ;;
        --version)
            (($# >= 2)) || fail 'Укажите --version vX.Y.Z.'
            release_version=$2
            shift 2
            ;;
        *)
            fail "Неизвестный аргумент: $1"
            ;;
    esac
done

[[ $ref =~ ^[a-zA-Z0-9][a-zA-Z0-9._/-]*$ && $ref != *..* ]] ||
    fail 'Некорректная Git-ссылка.'
[[ $release_version == latest || $release_version =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]] || fail 'Некорректная версия релиза.'
[[ $source_mode == false || $release_version == latest ]] || fail 'Не совмещайте --version с --source/--ref.'

for utility in \
    apt-get dpkg dpkg-query mktemp install mv ln uname \
    awk grep mkdir rm cat sleep readlink
do
    command -v "$utility" >/dev/null ||
        fail "Не найдена системная утилита $utility."
done

printf '  %s✓%s Система: %s %s (%s)\n' \
    "$GREEN" "$RESET" "$ID" "$VERSION_ID" "$arch"

section '[1/5] Проверка зависимостей'

missing=()

for pair in \
    curl:curl \
    jq:jq \
    sha256sum:coreutils \
    flock:util-linux
do
    command -v "${pair%%:*}" >/dev/null ||
        missing+=("${pair#*:}")
done
if $source_mode; then
    for pair in tar:tar gzip:gzip; do
        command -v "${pair%%:*}" >/dev/null || missing+=("${pair#*:}")
    done
fi

if ! dpkg-query -W -f='${Status}' ca-certificates \
    2>/dev/null | grep -q 'install ok installed'
then
    missing+=(ca-certificates)
fi

# Prepare workspace for step logs.
install -d -m 0755 /usr/local/lib/remnamanager /usr/local/bin

exec 9>/usr/local/lib/remnamanager/install.lock
flock -n 9 || fail 'Другой установщик уже работает.'
exec 8>/usr/local/lib/remnamanager/remanager.update.lock
flock -n 8 || fail 'Сейчас выполняется обновление менеджера.'
[[ ! -e /usr/local/lib/remnamanager/remanager.update.json ]] || fail 'Сначала завершите восстановление обновления менеджера.'

temporary=$(mktemp -d /usr/local/lib/remnamanager/install.XXXXXXXX)

cleanup() {
    case $(readlink -f "$temporary") in
        "$(readlink -f /usr/local/lib/remnamanager)"/install.*) rm -rf -- "$temporary" ;;
        *) printf 'Неожиданный временный путь; автоматическая очистка отменена.\n' >&2 ;;
    esac
}

trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM HUP

if ((${#missing[@]})); then
    run_step 'Обновление индекса пакетов' apt-get update

    run_step 'Установка системных зависимостей' \
        env DEBIAN_FRONTEND=noninteractive \
        apt-get install -y --no-install-recommends "${missing[@]}"
else
    printf '  %s✓%s Все зависимости установлены\n' "$GREEN" "$RESET"
fi

if $source_mode; then
section '[2/5] Подготовка исходного кода (явно выбранная сборка)'

if [[ -z $source_dir ]]; then
    printf '  %sРепозиторий:%s ConsolePro/RemnaManager\n' "$GRAY" "$RESET"
    printf '  %sВетка:%s %s\n' "$GRAY" "$RESET" "$ref"

    run_step 'Загрузка исходного кода' \
        download \
        "https://codeload.github.com/ConsolePro/RemnaManager/tar.gz/$ref" \
        "$temporary/source.tar.gz"

    mkdir "$temporary/source"

    run_step 'Распаковка исходного кода' \
        tar -xzf "$temporary/source.tar.gz" \
        --strip-components=1 \
        --no-same-owner \
        -C "$temporary/source"

    source_dir=$temporary/source
else
    source_dir=$(cd -- "$source_dir" && pwd)

    printf '  %s✓%s Использование локальных исходников\n' \
        "$GREEN" "$RESET"
fi

[[ -f $source_dir/go.mod && -f $source_dir/cmd/remna/main.go ]] ||
    fail 'В этой Git-ссылке нет Go-версии. Для прототипа укажите --ref feature/go-tui.'

required=$(awk '$1=="go" {print $2; exit}' "$source_dir/go.mod")

[[ $required =~ ^[0-9]+\.[0-9]+(\.[0-9]+)?$ ]] ||
    fail 'Некорректная версия Go в go.mod.'

printf '  %s✓%s Структура проекта проверена\n' "$GREEN" "$RESET"


section '[3/5] Подготовка Go SDK'

go_bin=$(command -v go || true)
existing=''

if [[ -n $go_bin ]]; then
    existing=$("$go_bin" version |
        awk '{sub(/^go/,"",$3); print $3}')
fi

if [[ -z $existing ]] ||
    ! dpkg --compare-versions "$existing" ge "$required"
then
    printf '  %sТребуется Go:%s %s или новее\n' \
        "$GRAY" "$RESET" "$required"

    run_step 'Получение информации о Go SDK' \
        download 'https://go.dev/dl/?mode=json' "$temporary/go.json"

    sdk=$(jq -er '[.[] | select(.stable)][0].version' \
        "$temporary/go.json")

    dpkg --compare-versions "${sdk#go}" ge "$required" ||
        fail 'Требуется более новая версия Go.'

    filename=$sdk.linux-$arch.tar.gz

    digest=$(jq -er --arg name "$filename" \
        '.[] | .files[] | select(.filename==$name) | .sha256' \
        "$temporary/go.json")

    [[ $digest =~ ^[0-9a-f]{64}$ ]] ||
        fail 'Не найден SHA256 Go SDK.'

    printf '  %sВыбрана версия:%s %s\n' "$GRAY" "$RESET" "$sdk"

    run_step 'Загрузка Go SDK' \
        download \
        "https://go.dev/dl/$filename" \
        "$temporary/go.tar.gz"

    verify_sdk() {
        printf '%s  %s\n' "$digest" "$temporary/go.tar.gz" |
            sha256sum -c -
    }

    run_step 'Проверка контрольной суммы SHA256' verify_sdk

    run_step 'Распаковка Go SDK' \
        tar -xzf "$temporary/go.tar.gz" \
        --no-same-owner \
        -C "$temporary"

    go_bin=$temporary/go/bin/go
else
    printf '  %s✓%s Go %s уже установлен\n' \
        "$GREEN" "$RESET" "$existing"
fi


section '[4/5] Сборка RemnaManager'

build_project() {
    cd -- "$source_dir"

    export CGO_ENABLED=0
    export GOTOOLCHAIN=local
    export GOPATH=$temporary/gopath
    export GOCACHE=$temporary/gocache

    "$go_bin" mod download
    "$go_bin" mod verify
    local build_version=v1.0.0
    if [[ -r $source_dir/VERSION ]]; then
        build_version=v$(tr -d '\r\n' <"$source_dir/VERSION")
        [[ $build_version =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]] || fail 'Invalid application VERSION.'
    fi
    "$go_bin" build \
        -mod=readonly \
        -trimpath \
        -ldflags "-X github.com/ConsolePro/RemnaManager/internal/updater.Version=$build_version" \
        -o "$temporary/remna" \
        ./cmd/remna
}

run_step 'Загрузка, проверка модулей и компиляция Go TUI' \
    build_project
else
    section '[2/5] Выбор готового релиза'
    endpoint="https://api.github.com/repos/$repository/releases/latest"
    [[ $release_version == latest ]] || endpoint="https://api.github.com/repos/$repository/releases/tags/$release_version"
    run_step 'Получение информации о релизе' download "$endpoint" "$temporary/release.json"
    version=$(jq -er 'select(.draft == false and .prerelease == false) | .tag_name' "$temporary/release.json")
    [[ $version =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]] || fail 'Некорректный тег релиза.'
    [[ $release_version == latest || $release_version == "$version" ]] || fail 'GitHub вернул другую версию.'
    filename=remanager-linux-$arch
    expected_url="https://github.com/$repository/releases/download/$version/$filename"
    url=$(jq -er --arg name "$filename" '[.assets[] | select(.name == $name)] | select(length == 1) | .[0].browser_download_url' "$temporary/release.json") || fail 'В релизе нет готового Go-бинарника для этой архитектуры.'
    [[ $url == "$expected_url" ]] || fail 'Некорректный адрес бинарника в релизе.'
    printf '  Версия: %s · архитектура: %s\n' "$version" "$arch"

    section '[3/5] Проверка контрольной суммы'
    digest=$(jq -r --arg name "$filename" '.assets[] | select(.name == $name) | .digest // ""' "$temporary/release.json")
    if [[ $digest == sha256:* ]]; then
        digest=${digest#sha256:}
    else
        checksum_url=$(jq -er '[.assets[] | select(.name == "checksums.txt")] | select(length == 1) | .[0].browser_download_url' "$temporary/release.json") || fail 'В релизе нет контрольных сумм.'
        [[ $checksum_url == "https://github.com/$repository/releases/download/$version/checksums.txt" ]] || fail 'Некорректный адрес контрольных сумм.'
        run_step 'Загрузка SHA256' download "$checksum_url" "$temporary/checksums.txt"
        digest=$(awk -v name="$filename" '{file=$2; sub(/^\*/, "", file); if (file==name) {sum=$1; count++}} END {if (count!=1) exit 1; print sum}' "$temporary/checksums.txt") || fail 'Отсутствует однозначная SHA256 бинарника.'
    fi
    [[ $digest =~ ^[a-f0-9]{64}$ ]] || fail 'Некорректная SHA256 бинарника.'
    run_step 'Загрузка готового RemnaManager' download "$url" "$temporary/remna"
    verify_binary() { printf '%s  %s\n' "$digest" "$temporary/remna" | sha256sum -c -; }
    run_step 'Проверка SHA256 бинарника' verify_binary
    chmod 0755 "$temporary/remna"

    section '[4/5] Проверка готового приложения'
    actual_version=$("$temporary/remna" --version | awk 'NR==1 {print $1}')
    [[ $actual_version == "$version" ]] || fail 'Версия бинарника не совпадает с тегом релиза.'
    "$temporary/remna" --inspect-manager "$temporary/remna" >/dev/null || fail 'Файл релиза не является Go-менеджером RemnaManager.'
fi

run_step 'Проверка запуска RemnaManager' \
    "$temporary/remna" --version


section '[5/5] Установка приложения'

# Keep the manager executable outside the panel's namespace.
for shortcut in remanager re; do
    path=/usr/local/bin/$shortcut

    if [[ -e $path || -L $path ]]; then
        target=$(readlink "$path" || true)

        case "$target" in
            remna|remanager|../lib/remnamanager/remanager) ;;
            *)
                fail "Команда $path уже занята другим файлом. Она не перезаписана."
                ;;
        esac
    fi
done

manager_path=/usr/local/lib/remnamanager/remanager
if [[ -e $manager_path || -L $manager_path ]]; then
    [[ -f $manager_path && ! -L $manager_path ]] || fail 'Некорректный путь установленного менеджера.'
    "$temporary/remna" --inspect-manager "$manager_path" >/dev/null || fail 'Установленный файл не является Go-менеджером RemnaManager.'
    previous_version=$("$manager_path" --version | awk 'NR==1 {print $1}')
    [[ $previous_version =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]] || fail 'Не удалось определить предыдущую версию.'
    previous_digest=$(sha256sum "$manager_path"); previous_digest=${previous_digest%% *}
    install -m 0755 "$manager_path" "$temporary/previous"
    mv -f -- "$temporary/previous" "$manager_path.previous"
    jq -n --arg Version "$previous_version" --arg SHA256 "$previous_digest" '{Version:$Version,SHA256:$SHA256}' >"$temporary/previous.json"
    install -m 0600 "$temporary/previous.json" "$manager_path.previous.json"
fi

run_step 'Установка исполняемого файла' \
    install -m 0755 \
    "$temporary/remna" \
    /usr/local/lib/remnamanager/.remanager.new

run_step 'Активация новой версии' \
    mv -f -- \
    /usr/local/lib/remnamanager/.remanager.new \
    /usr/local/lib/remnamanager/remanager
if ! "$manager_path" --version >/dev/null; then
    if [[ -f $manager_path.previous ]]; then
        install -m 0755 "$manager_path.previous" "$temporary/restore"
        mv -f -- "$temporary/restore" "$manager_path"
    fi
    fail 'Новое приложение не запустилось; предыдущая версия восстановлена, если была установлена.'
fi

run_step 'Регистрация команды remanager' \
    ln -sfn \
    ../lib/remnamanager/remanager \
    /usr/local/bin/remanager

run_step 'Регистрация короткой команды re' \
    ln -sfn remanager /usr/local/bin/re

# Migrate only the old Go manager.
# Commands belonging to Remnawave or other programs remain intact.
if [[ -f /usr/local/bin/remna && ! -L /usr/local/bin/remna ]] &&
    "$manager_path" --inspect-manager /usr/local/bin/remna >/dev/null 2>&1
then
    run_step 'Сохранение предыдущей версии менеджера' \
        mv -f -- \
        /usr/local/bin/remna \
        /usr/local/lib/remnamanager/remna.previous
fi

if [[ -L /usr/local/bin/remna_manager ]] &&
    [[ $(readlink /usr/local/bin/remna_manager) == remna ]]
then
    run_step 'Обновление прежнего ярлыка' \
        ln -sfn remanager /usr/local/bin/remna_manager
fi

run_step 'Подготовка каталога конфигурации' \
    install -d -m 0700 /etc/remnamanager


printf '\n'
printf '%s━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━%s\n' "$GREEN" "$RESET"
printf '%s  ✓ RemnaManager успешно установлен%s\n' "$GREEN" "$RESET"
printf '%s━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━%s\n' "$GREEN" "$RESET"

printf '\n  Запуск:      %sre%s или %sremanager%s\n' \
    "$BOLD" "$RESET" "$BOLD" "$RESET"

printf '  Конфигурация: /etc/remnamanager/config.json\n'
printf '  Существующие настройки сохранены.\n\n'
