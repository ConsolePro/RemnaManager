#!/usr/bin/env bash
platform_check() {
    local id version codename
    [[ $(uname -s) == Linux && -f /etc/os-release ]] || die 'Нужен Linux.'
    id=$(sed -n 's/^ID=//p' /etc/os-release | tr -d '"')
    version=$(sed -n 's/^VERSION_ID=//p' /etc/os-release | tr -d '"')
    codename=$(sed -n 's/^VERSION_CODENAME=//p' /etc/os-release | tr -d '"')
    case $id:$version in debian:12|debian:13|ubuntu:22.04|ubuntu:24.04) ;; *) die 'Поддерживаются Debian 12/13 и Ubuntu 22.04/24.04.';; esac
    [[ $codename =~ ^[a-z]+$ ]] || die 'Не определён код релиза ОС.'
    OS_ID=$id OS_CODENAME=$codename
}
bootstrap() {
    platform_check
    if ! command -v jq >/dev/null || ! command -v curl >/dev/null || ! command -v openssl >/dev/null || ! command -v ssh-keygen >/dev/null; then
        apt-get update
        DEBIAN_FRONTEND=noninteractive apt-get install -y ca-certificates curl jq openssl openssh-client
    fi
}
nginx_reload() { nginx -t && systemctl reload nginx; }
nginx_write() {
    local path=$1 old existed=0
    old=$(mktemp "$ROOT/nginx-backup.XXXXXXXX")
    if [[ -f $path ]]; then cp -- "$path" "$old"; existed=1; fi
    save "$path"
    if nginx_reload; then rm -f -- "$old"; return; fi
    if ((existed)); then mv -f -- "$old" "$path"; else rm -f -- "$path" "$old"; fi
    nginx_reload || true
    die 'Nginx отклонил конфигурацию; прежняя конфигурация восстановлена.'
}
packages() {
    local role=${1:-panel} common=/etc/nginx/conf.d/remnamanager-common.conf reject version
    platform_check
    apt-get update
    DEBIAN_FRONTEND=noninteractive apt-get install -y ca-certificates curl jq openssl gnupg nginx certbot openssh-client nftables unzip util-linux
    if command -v docker >/dev/null; then docker compose version
    else
        mkdir -p /etc/apt/keyrings
        chmod 755 /etc/apt/keyrings
        download "https://download.docker.com/linux/$OS_ID/gpg" /etc/apt/keyrings/docker.asc
        chmod 644 /etc/apt/keyrings/docker.asc
        printf 'deb [arch=%s signed-by=/etc/apt/keyrings/docker.asc] https://download.docker.com/linux/%s %s stable\n' "$(dpkg --print-architecture)" "$OS_ID" "$OS_CODENAME" | save /etc/apt/sources.list.d/remnamanager-docker.list
        chmod 644 /etc/apt/sources.list.d/remnamanager-docker.list
        apt-get update
        DEBIAN_FRONTEND=noninteractive apt-get install -y docker-ce docker-ce-cli containerd.io docker-compose-plugin
    fi
    systemctl enable --now docker nginx
    if [[ -L /etc/nginx/sites-enabled/default && $(readlink -f /etc/nginx/sites-enabled/default) == /etc/nginx/sites-available/default ]]; then rm /etc/nginx/sites-enabled/default; fi
    if [[ ! -f $common ]]; then
        reject='ssl_reject_handshake on;'
        version=$(nginx -v 2>&1 | sed -n 's/.*nginx\/\([0-9.]*\).*/\1/p')
        if [[ $role == panel ]] && ! dpkg --compare-versions "${version:-0}" ge 1.19.4; then
            if [[ ! -f $ROOT/default.key || ! -f $ROOT/default.crt ]]; then
                openssl req -x509 -newkey rsa:2048 -nodes -days 3650 -subj /CN=localhost -keyout "$ROOT/default.key" -out "$ROOT/default.crt" >/dev/null 2>&1
            fi
            reject="ssl_certificate $ROOT/default.crt; ssl_certificate_key $ROOT/default.key; return 444;"
        fi
        {
            printf '%s\n' 'map_hash_bucket_size 128;' "map \$http_upgrade \$rm_connection { default upgrade; '' close; }" 'server { listen 80 default_server; listen [::]:80 default_server; server_name _; return 444; }'
            if [[ $role == panel ]]; then printf 'server { listen 443 ssl default_server; listen [::]:443 ssl default_server; server_name _; %s }\n' "$reject"; fi
        } | save "$common"
    fi
    # Older installations may already contain the common file without enough
    # map capacity for the 64-character access cookie. Upgrade it in place.
    if ! grep -q '^[[:space:]]*map_hash_bucket_size' "$common"; then
        { printf 'map_hash_bucket_size 128;\n'; cat "$common"; } | save "$common"
    fi
    nginx_reload
    printf '%s\n' '#!/bin/sh' 'set -eu' '/usr/sbin/nginx -t' '/bin/systemctl reload nginx' | save /etc/letsencrypt/renewal-hooks/deploy/remnamanager-nginx
    chmod 700 /etc/letsencrypt/renewal-hooks/deploy/remnamanager-nginx
    systemctl enable --now certbot.timer
}
site() {
    local name email=$2 upstream=${3:-} gate=${4:-} role=${5:-panel} dest conf
    name=$(domain "$1")
    [[ $email =~ ^[^[:space:]@]+@[^[:space:]@]+\.[^[:space:]@]+$ && $email != -* ]] || die 'Неверный email ACME.'
    getent ahosts "$name" >/dev/null || die 'DNS домена не разрешается.'
    dest=/var/www/remnamanager/$name conf=/etc/nginx/conf.d/rm-$name.conf
    mkdir -p /var/www/remnamanager-acme "$dest"
    chmod 755 /var/www/remnamanager-acme /var/www/remnamanager "$dest"
    if [[ ! -f $dest/index.html ]]; then site_html | save "$dest/index.html"; chmod 644 "$dest/index.html"; fi
    if [[ ! -f $conf ]]; then http_site "$name" | nginx_write "$conf"; fi
    certbot certonly --webroot -w /var/www/remnamanager-acme -d "$name" --email "$email" --agree-tos --non-interactive --keep-until-expiring
    { http_site "$name"; if [[ $role == node ]]; then tls_site "$name" 8443; else tls_site "$name" 443 "$upstream" "$gate"; fi; } | nginx_write "$conf"
}
firewall() {
    local ip family=ip
    ip=$(valid_ip "$1")
    [[ $ip != *:* ]] || family=ip6
    cat <<EOF | save "$ROOT/node-firewall.nft"
table inet remnamanager {
 chain input { type filter hook input priority -10; policy accept;
  iifname "lo" accept
  tcp dport 2222 $family saddr $ip accept
  tcp dport 2222 drop
 }
}
EOF
    cat <<'EOF' | save "$ROOT/node-firewall.sh"
#!/bin/sh
set -eu
if nft list table inet remnamanager >/dev/null 2>&1; then
 { echo 'delete table inet remnamanager'; cat /opt/remnamanager/node-firewall.nft; } | nft -f -
else nft -f /opt/remnamanager/node-firewall.nft
fi
EOF
    chmod 700 "$ROOT/node-firewall.sh"
    cat <<EOF | save /etc/systemd/system/remnamanager-firewall.service
[Unit]
Description=RemnaManager node API restriction
After=network-pre.target nftables.service
Before=docker.service
[Service]
Type=oneshot
RemainAfterExit=yes
ExecStart=$ROOT/node-firewall.sh
ExecReload=$ROOT/node-firewall.sh
[Install]
WantedBy=multi-user.target
EOF
    systemctl daemon-reload
    systemctl enable remnamanager-firewall
    "$ROOT/node-firewall.sh"
}
node_install() {
    local job=$1
    [[ ! -f /opt/remnawave/docker-compose.yml ]] || die 'Нода должна быть на отдельном сервере: найдена панель.'
    [[ ! -d /opt/remnanode || -f /opt/remnanode/.remnamanager ]] || die '/opt/remnanode не управляется RemnaManager.'
    packages node
    firewall "$(jq -er .panel_ip <<<"$job")"
    site "$(jq -er .domain <<<"$job")" "$(jq -er .email <<<"$job")" '' '' node
    mkdir -p /opt/remnanode
    printf 'managed\n' | save /opt/remnanode/.remnamanager
    if [[ -f /opt/remnanode/compose.yaml ]]; then cp /opt/remnanode/compose.yaml /opt/remnanode/compose.previous.json; fi
    node_compose "$(jq -er .secret <<<"$job")" | save /opt/remnanode/compose.yaml
    compose /opt/remnanode config --quiet
    compose /opt/remnanode pull
    compose /opt/remnanode up -d --wait --wait-timeout 120
    jq '{panel_ip,domain,email}' <<<"$job" | save "$ROOT/node.json"
}
warp_install() {
    local attempt
    platform_check
    mkdir -p /etc/apt/keyrings
    chmod 755 /etc/apt/keyrings
    download https://pkg.cloudflareclient.com/pubkey.gpg "$ROOT/cloudflare-key.asc"
    gpg --batch --yes --dearmor --output /etc/apt/keyrings/cloudflare-warp.gpg "$ROOT/cloudflare-key.asc"
    chmod 644 /etc/apt/keyrings/cloudflare-warp.gpg
    printf 'deb [signed-by=/etc/apt/keyrings/cloudflare-warp.gpg] https://pkg.cloudflareclient.com/ %s main\n' "$OS_CODENAME" | save /etc/apt/sources.list.d/remnamanager-warp.list
    chmod 644 /etc/apt/sources.list.d/remnamanager-warp.list
    apt-get update
    DEBIAN_FRONTEND=noninteractive apt-get install -y cloudflare-warp
    systemctl enable --now warp-svc
    if ! warp-cli --accept-tos registration show >/dev/null 2>&1; then warp-cli --accept-tos registration new; fi
    warp-cli --accept-tos mode proxy
    warp-cli --accept-tos proxy port 40000
    warp-cli --accept-tos connect
    for ((attempt=0;attempt<12;attempt++)); do
        if curl --fail --silent --max-time 10 --socks5-hostname 127.0.0.1:40000 https://www.cloudflare.com/cdn-cgi/trace | grep -q '^warp=on'; then return; fi
        sleep 2
    done
    die 'WARP не прошёл проверку. Маршрутизация Xray не изменена.'
}
xray_update() {
    local asset tag archive checksums digest target override=/opt/remnanode/compose.override.yaml old existed=0 merged
    download https://api.github.com/repos/XTLS/Xray-core/releases/latest "$ROOT/xray-release.json"
    case $(uname -m) in x86_64) asset=Xray-linux-64.zip;; aarch64) asset=Xray-linux-arm64-v8a.zip;; *) die 'Xray поддерживает amd64 и arm64.';; esac
    tag=$(jq -er .tag_name "$ROOT/xray-release.json")
    [[ $tag =~ ^[a-zA-Z0-9._-]+$ && $tag != .* ]] || die 'Неверный tag Xray.'
    archive=$ROOT/$asset checksums=$ROOT/$asset.dgst
    download "$(jq -er --arg name "$asset" '.assets[] | select(.name == $name) | .browser_download_url' "$ROOT/xray-release.json")" "$archive"
    download "$(jq -er --arg name "$asset.dgst" '.assets[] | select(.name == $name) | .browser_download_url' "$ROOT/xray-release.json")" "$checksums"
    digest=$(sha256sum "$archive"); digest=${digest%% *}
    grep -Eiq "(^|[^[:xdigit:]])$digest([^[:xdigit:]]|$)" "$checksums" || die 'SHA256 Xray не совпадает.'
    mkdir -p /opt/remnanode/xray
    chmod 755 /opt/remnanode/xray
    target=/opt/remnanode/xray/$tag
    unzip -p "$archive" xray | save "$target"
    chmod 755 "$target"
    "$target" version
    old=$(mktemp "$ROOT/xray-override.XXXXXXXX")
    if [[ -f $override ]]; then
        cp "$override" "$old"; existed=1
        jq -e 'type == "object"' "$old" >/dev/null || die 'Найден сторонний YAML override. Объедините настройки вручную.'
    else printf '{}\n' >"$old"; fi
    merged=$(jq --arg mount "$target:/usr/local/bin/xray:ro" '.services.remnanode.volumes = ([.services.remnanode.volumes[]? | select(if type == "string" then (endswith(":/usr/local/bin/xray:ro")|not) else .target != "/usr/local/bin/xray" end)] + [$mount])' "$old")
    printf '%s\n' "$merged" | save "$override"
    if compose /opt/remnanode up -d --force-recreate --wait --wait-timeout 90 && sleep 4 && docker exec remnanode /usr/local/bin/xray version; then rm -f "$old"; return; fi
    if ((existed)); then mv -f "$old" "$override"; else rm -f "$override" "$old"; fi
    compose /opt/remnanode up -d --force-recreate
    die 'Xray не запустился. Прежний override восстановлен.'
}
execute_job() {
    local job=$1 action name html
    action=$(jq -er .action <<<"$job")
    case $action in
        install) node_install "$job";;
        update) compose /opt/remnanode pull; compose /opt/remnanode up -d --wait --wait-timeout 120;;
        restart|start|stop) compose /opt/remnanode "$action";;
        status) compose /opt/remnanode ps; compose /opt/remnanode logs --tail 50 --no-color;;
        template)
            name=$(domain "$(json_get "$ROOT/node.json" .domain)")
            if jq -e 'has("html")' <<<"$job" >/dev/null; then
                html=$(jq -er .html <<<"$job"); [[ ${#html} -le 2097152 ]] || die 'Лимит HTML: 2 МБ.'
                printf '%s\n' "$html" | save "/var/www/remnamanager/$name/index.html"
            else site_html "$(jq -er .style <<<"$job")" | save "/var/www/remnamanager/$name/index.html"; fi
            chmod 644 "/var/www/remnamanager/$name/index.html";;
        xray) xray_update;;
        warp-install) warp_install;;
        warp-remove) warp-cli --accept-tos disconnect; apt-get remove -y cloudflare-warp;;
        bbr)
            modprobe tcp_bbr
            printf 'net.core.default_qdisc=fq\nnet.ipv4.tcp_congestion_control=bbr\n' | save /etc/sysctl.d/90-remnamanager-bbr.conf
            sysctl -p /etc/sysctl.d/90-remnamanager-bbr.conf;;
        *) die 'Неизвестная операция агента.';;
    esac
}
