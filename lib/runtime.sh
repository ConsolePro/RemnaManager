#!/usr/bin/env bash
# Shared Shell runtime. Stored JSON is data and is never sourced or evaluated.
ROOT=${RM_ROOT:-/opt/remnamanager}
PANEL=${RM_PANEL:-/opt/remnawave}
SUB=$PANEL/subscription
CHECKER=$ROOT/checker
STATE=$ROOT/state.json

die() { printf 'Ошибка: %s\n' "$*" >&2; exit 1; }
need() { command -v "$1" >/dev/null 2>&1 || die "Не найдена команда: $1"; }
save() {
    local path=$1 temporary
    mkdir -p -- "$(dirname -- "$path")"
    temporary=$(mktemp "${path}.tmp.XXXXXXXX")
    if cat >"$temporary" && chmod 600 "$temporary" && mv -f -- "$temporary" "$path"; then return; fi
    rm -f -- "$temporary"
    return 1
}
json_get() { jq -er "$2 // empty" "$1"; }
state_get() { jq -r "$1 // empty" "$STATE"; }
json_edit() {
    local file=$1 filter=$2 result
    shift 2
    result=$(jq "$@" "$filter" "$file")
    printf '%s\n' "$result" | save "$file"
}
state_edit() { json_edit "$STATE" "$@"; }
random_hex() { openssl rand -hex "${1:-32}"; }
ask() {
    local label=$1 default=${2:-} secret=${3:-} value
    printf '%s%s: ' "$label" "${default:+ [$default]}" >&2
    if [[ $secret == secret ]]; then IFS= read -r -s value || exit 1; printf '\n' >&2
    else IFS= read -r value || exit 1; fi
    value=${value:-$default}
    [[ -n $value && $value != *$'\r'* && $value != *$'\n'* ]] || die 'Пустое значение или недопустимый перевод строки.'
    printf '%s\n' "$value"
}
confirm() {
    local answer
    printf '%s Введите ДА: ' "$*" >&2
    IFS= read -r answer || return 1
    [[ $answer == ДА ]]
}
menu() {
    local title=$1 item
    shift
    printf '\n%s\n' "$title" >&2
    for item in "$@"; do printf '%s\n' "$item" >&2; done
    printf '> ' >&2
    IFS= read -r CHOICE || CHOICE=0
}
domain() {
    local value label
    value=$(printf '%s' "$1" | tr '[:upper:]' '[:lower:]' | sed 's/^[[:space:]]*//;s/[[:space:]]*$//')
    [[ ${#value} -le 253 && $value == *.* && $value =~ ^[a-z0-9.-]+$ ]] || die 'Нужен DNS-домен без схемы и пути.'
    local -a labels
    IFS=. read -r -a labels <<<"$value"
    [[ $value != *. && ${labels[-1]} =~ ^[a-z] ]] || die 'Неверный DNS-домен.'
    for label in "${labels[@]}"; do
        [[ ${#label} -le 63 && $label =~ ^[a-z0-9]([a-z0-9-]*[a-z0-9])?$ ]] || die 'Неверная метка DNS.'
    done
    printf '%s\n' "$value"
}
host() { [[ $1 =~ ^[a-zA-Z0-9][a-zA-Z0-9.:-]{0,252}$ ]] || die 'Неверный SSH-адрес.'; printf '%s\n' "$1"; }
port() {
    [[ $1 =~ ^[0-9]{1,5}$ ]] || die 'Порт должен быть числом.'
    ((10#$1 >= 1 && 10#$1 <= 65535)) || die 'Порт должен быть от 1 до 65535.'
    printf '%d\n' "$((10#$1))"
}
valid_uuid() { [[ $1 =~ ^[a-fA-F0-9]{8}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{12}$ ]] || die 'Некорректный UUID API.'; }
valid_ip() {
    local value=$1 octet
    if [[ $value == *:* ]]; then
        [[ $value =~ ^[0-9a-fA-F:]+$ && ${#value} -le 39 ]] || die 'Неверный IPv6.'
        # libc validates compressed IPv6 as well as full addresses; no DNS accepted.
        getent ahosts "$value" >/dev/null || getent hosts "$value" >/dev/null || die 'Неверный IP-адрес.'
    else
        [[ $value =~ ^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$ ]] || die 'Неверный IPv4.'
        local -a octets
        IFS=. read -r -a octets <<<"$value"
        for octet in "${octets[@]}"; do
            [[ ${#octet} -le 3 ]] || die 'Неверный IPv4.'
            ((10#$octet <= 255)) || die 'Неверный IPv4.'
        done
    fi
    printf '%s\n' "$value"
}
env_set() {
    local path=$1 key=$2 value=$3
    [[ $key =~ ^[A-Z_][A-Z_0-9]*$ && $value != *$'\n'* && $value != *$'\r'* ]] || die 'Неверное значение .env.'
    { if [[ -f $path ]]; then awk -v k="$key" 'index($0,k "=") != 1' "$path"; fi; printf '%s=%s\n' "$key" "$value"; } | save "$path"
}
compose() { local directory=$1; shift; docker compose --project-directory "$directory" "$@"; }
download() {
    local url=$1 target=$2 temporary
    mkdir -p -- "$(dirname -- "$target")"
    temporary=$(mktemp "${target}.download.XXXXXXXX")
    if curl --fail --silent --show-error --location --proto '=https' --proto-redir '=https' --connect-timeout 15 --max-time 180 --max-filesize 134217728 "$url" -o "$temporary"; then
        chmod 600 "$temporary"; mv -f -- "$temporary" "$target"
    else rm -f -- "$temporary"; return 1; fi
}
api() {
    local method=$1 path=$2 body=${3:-} response headers code token
    token=${API_TOKEN:-$(state_get '.token')}
    if [[ ${API_ANONYMOUS:-0} == 1 ]]; then token=''; fi
    [[ -n $token || ${API_ANONYMOUS:-0} == 1 ]] || die 'Сначала установите панель или смените API-токен в CLI.'
    headers=$(mktemp "$ROOT/api-headers.XXXXXXXX")
    response=$(mktemp "$ROOT/api-response.XXXXXXXX")
    { printf 'Content-Type: application/json\nX-Forwarded-Proto: https\nX-Forwarded-For: 127.0.0.1\n';
      if [[ ${API_ADMIN:-0} == 1 ]]; then printf 'X-Remnawave-Client-Type: browser\n'; fi
      [[ -z $token ]] || printf 'Authorization: Bearer %s\n' "$token"; } >"$headers"
    local -a args=(--silent --show-error --noproxy '127.0.0.1,localhost,::1' --connect-timeout 10 --max-time 40 -X "$method" -H "@$headers" -o "$response" -w '%{http_code}')
    if [[ -n $body ]]; then args+=(--data-binary @-); fi
    if ! code=$(printf '%s' "$body" | curl "${args[@]}" "http://127.0.0.1:3000/api$path"); then
        rm -f -- "$headers" "$response"; die "API $method $path: ошибка соединения."
    fi
    rm -f -- "$headers"
    if [[ $code != 2?? ]]; then rm -f -- "$response"; die "API $method $path: HTTP $code. Проверьте версию панели и токен."; fi
    if [[ -s $response ]]; then
        if ! jq -e 'has("response")' "$response" >/dev/null; then rm -f -- "$response"; die 'API: неожиданный формат ответа.'; fi
        jq '.response' "$response"
    fi
    rm -f -- "$response"
}
wait_http() {
    local url=$1 attempt code
    shift
    for ((attempt=0; attempt<${RM_HTTP_ATTEMPTS:-30}; attempt++)); do
        code=$(curl --silent --noproxy 127.0.0.1,localhost,::1 --max-time 3 -o /dev/null -w '%{http_code}' "$@" "$url") || code=000
        case $code in 2??|3??|400|401|403|404) return;; esac
        sleep 2
    done
    die "Приложение не отвечает: $url (последний HTTP-код: $code). Проверьте логи контейнера."
}
nodes() { api GET /nodes | jq 'if type == "array" then . else .nodes end'; }
