#!/usr/bin/env bash
# Native WireGuard design adapted from distillium/warp-native (MIT).
# See LICENSES/warp-native-MIT.txt. No Cloudflare client or SOCKS proxy is used.
warp_profile() {
    local profile=$1 private public address
    private=$(awk '/^PrivateKey *=/{sub(/^[^=]*=[[:space:]]*/,"");print;exit}' "$profile")
    public=$(awk '/^PublicKey *=/{sub(/^[^=]*=[[:space:]]*/,"");print;exit}' "$profile")
    address=$(awk '/^Address *=/{sub(/^[^=]*=[[:space:]]*/,"");split($0,a,",");gsub(/ /,"",a[1]);print a[1];exit}' "$profile")
    [[ $private =~ ^[A-Za-z0-9+/]{43}=$ && $public =~ ^[A-Za-z0-9+/]{43}=$ && $address =~ ^[0-9.]+/32$ ]] || die 'wgcf вернул некорректный профиль WireGuard.'
    cat <<EOF
[Interface]
PrivateKey = $private
Address = $address
MTU = 1280
Table = off
PostUp = ip -4 route replace default dev %i table 51888
PostUp = ip -4 rule add priority 51888 fwmark 51888 lookup 51888
PostUp = ip -4 rule add priority 51889 from $address lookup 51888
PostDown = ip -4 rule del priority 51888 fwmark 51888 lookup 51888 || true
PostDown = ip -4 rule del priority 51889 from $address lookup 51888 || true
PostDown = ip -4 route flush table 51888
[Peer]
PublicKey = $public
AllowedIPs = 0.0.0.0/0
Endpoint = 162.159.192.1:2408
PersistentKeepalive = 25
EOF
}
warp_killswitch_files() {
    cat <<'EOF' | save "$ROOT/warp-native/killswitch.sh"
#!/bin/sh
set -eu
exec 9>/opt/remnamanager/warp-native/killswitch.lock
flock 9
if nft list table inet remnamanager_warp >/dev/null 2>&1; then
    printf 'delete table inet remnamanager_warp\n'
fi > /opt/remnamanager/warp-native/kill-rules.nft
cat >> /opt/remnamanager/warp-native/kill-rules.nft <<'NFT'
table inet remnamanager_warp {
 chain output { type filter hook output priority -150; policy accept;
  meta mark 51888 oifname != "warp" counter drop
 }
}
NFT
nft -f /opt/remnamanager/warp-native/kill-rules.nft
EOF
    chmod 700 "$ROOT/warp-native/killswitch.sh"
    cat <<EOF | save /etc/systemd/system/remnamanager-warp-killswitch.service
[Unit]
Description=RemnaManager WARP-Native kill-switch
After=nftables.service
Before=wg-quick@warp.service
[Service]
Type=oneshot
RemainAfterExit=yes
ExecStart=$ROOT/warp-native/killswitch.sh
ExecReload=$ROOT/warp-native/killswitch.sh
[Install]
WantedBy=multi-user.target
EOF
    cat <<EOF | save /etc/systemd/system/wg-quick@warp.service.d/remnamanager.conf
[Unit]
Requires=remnamanager-warp-killswitch.service
After=remnamanager-warp-killswitch.service
[Service]
ExecStartPre=$ROOT/warp-native/killswitch.sh
EOF
    cat <<EOF | save /etc/systemd/system/nftables.service.d/remnamanager-warp.conf
[Service]
ExecStartPost=$ROOT/warp-native/killswitch.sh
ExecReload=$ROOT/warp-native/killswitch.sh
EOF
}
warp_container_caps() {
    local remove=${1:-false} path=/opt/remnanode/compose.override.yaml current='{}' previous added merged
    [[ -f /opt/remnanode/compose.yaml || -f /opt/remnanode/docker-compose.yml ]] || die 'Сначала установите ноду.'
    if [[ -f $path ]]; then current=$(jq -e 'select(type=="object")' "$path") || die 'Сторонний YAML override: сначала объедините настройки вручную.'; fi
    previous=$(mktemp "$ROOT/warp-native/override.XXXXXXXX")
    printf '%s\n' "$current" >"$previous"
    if [[ $remove == false ]]; then
        added=$(jq '["NET_ADMIN","NET_RAW"]-(.services.remnanode.cap_add//[])' <<<"$current")
        if [[ ! -f $ROOT/warp-native/capabilities-added.json ]]; then printf '%s\n' "$added" | save "$ROOT/warp-native/capabilities-added.json"; fi
        merged=$(jq '.services.remnanode.cap_add=((.services.remnanode.cap_add//[])+["NET_ADMIN","NET_RAW"]|unique)' <<<"$current")
    else
        added='[]'
        if [[ -f $ROOT/warp-native/capabilities-added.json ]]; then added=$(cat "$ROOT/warp-native/capabilities-added.json"); fi
        merged=$(jq --argjson added "$added" '.services.remnanode.cap_add=((.services.remnanode.cap_add//[])-$added)' <<<"$current")
    fi
    printf '%s\n' "$merged" | save "$path"
    if compose /opt/remnanode config --quiet && compose /opt/remnanode up -d --wait --wait-timeout 120; then rm -f "$previous"; return; fi
    mv "$previous" "$path"
    compose /opt/remnanode up -d
    die 'Не удалось применить права контейнера WARP-Native. Прежний override возвращён.'
}
warp_install() {
    local directory=$ROOT/warp-native arch version name url digest actual attempt
    platform_check
    if [[ -f /etc/wireguard/warp.conf && ! -f $directory/managed ]]; then die 'Интерфейс warp уже настроен вне RemnaManager. Его конфигурация сохранена без изменений.'; fi
    mkdir -p "$directory" /etc/wireguard
    chmod 700 "$directory" /etc/wireguard
    step 'Обновляю пакеты WARP-Native' apt-get update
    DEBIAN_FRONTEND=noninteractive step 'Устанавливаю WireGuard' apt-get install -y --no-install-recommends wireguard-tools iproute2 nftables curl jq
    case $(uname -m) in x86_64) arch=amd64;; aarch64) arch=arm64;; *) die 'WARP-Native поддерживает amd64 и arm64.';; esac
    download https://api.github.com/repos/ViRb3/wgcf/releases/latest "$directory/release.json"
    version=$(json_get "$directory/release.json" .tag_name)
    [[ $version =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]] || die 'Некорректная версия wgcf.'
    name=wgcf_${version#v}_linux_$arch
    url=$(jq -er --arg name "$name" '.assets[]|select(.name==$name)|.browser_download_url' "$directory/release.json")
    digest=$(jq -er --arg name "$name" '.assets[]|select(.name==$name)|.digest // empty' "$directory/release.json")
    [[ $digest =~ ^sha256:[a-f0-9]{64}$ ]] || die 'Нет SHA256 wgcf. Установка остановлена до запуска бинарника.'
    download "$url" "$directory/wgcf"
    actual=$(sha256sum "$directory/wgcf"); actual=${actual%% *}
    [[ $actual == "${digest#sha256:}" ]] || die 'SHA256 wgcf не совпадает.'
    chmod 700 "$directory/wgcf"
    if [[ ! -f $directory/wgcf-account.toml ]]; then
        (cd "$directory" || exit; step 'Регистрирую аккаунт WARP-Native' timeout 120 ./wgcf register --accept-tos)
    fi
    (cd "$directory" || exit; step 'Генерирую профиль WireGuard' ./wgcf generate)
    warp_profile "$directory/wgcf-profile.conf" | save /etc/wireguard/warp.conf
    printf 'managed\n' | save "$directory/managed"
    warp_killswitch_files
    step 'Обновляю службы WARP-Native' systemctl daemon-reload
    step 'Включаю защиту от обхода WARP' systemctl enable --now remnamanager-warp-killswitch.service
    step 'Запускаю интерфейс WARP-Native' systemctl enable --now wg-quick@warp
    # Reapply an existing managed interface after regeneration.
    step 'Применяю профиль WireGuard' systemctl restart wg-quick@warp
    warp_container_caps
    for ((attempt=0;attempt<12;attempt++)); do
        if curl --fail --silent --max-time 8 --interface warp https://www.cloudflare.com/cdn-cgi/trace | grep -Eq '^warp=(on|plus)'; then
            printf 'WARP-Native: интерфейс warp работает.\n'; return
        fi
        sleep 2
    done
    die 'WARP-Native не прошёл сетевую проверку. Профиль Xray не изменён.'
}
warp_remove() {
    [[ -f $ROOT/warp-native/managed ]] || die 'Этот интерфейс WARP не управляется RemnaManager.'
    step 'Останавливаю интерфейс WARP-Native' systemctl disable --now wg-quick@warp
    step 'Останавливаю защиту WARP-Native' systemctl disable --now remnamanager-warp-killswitch.service
    # Keep the account and configuration backup for a future reinstall.
    if [[ -f /etc/wireguard/warp.conf ]]; then mv /etc/wireguard/warp.conf "$ROOT/warp-native/warp.previous.conf"; fi
    rm -f /etc/systemd/system/wg-quick@warp.service.d/remnamanager.conf /etc/systemd/system/nftables.service.d/remnamanager-warp.conf /etc/systemd/system/remnamanager-warp-killswitch.service
    if nft list table inet remnamanager_warp >/dev/null 2>&1; then step 'Удаляю правила WARP-Native' nft delete table inet remnamanager_warp; fi
    step 'Обновляю службы сервера' systemctl daemon-reload
    warp_container_caps true
    rm -f "$ROOT/warp-native/capabilities-added.json"
}
