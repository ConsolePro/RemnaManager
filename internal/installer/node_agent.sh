set -Eeuo pipefail
umask 077
[[ ${RM_JOB_ID:-} =~ ^[a-f0-9]{32}$ && ${RM_STAGE:-} =~ ^[0-8]$ ]] || exit 2
[[ $(id -u) == 0 && -f /etc/os-release && -d /run/systemd/system ]] || { echo 'Linux root and systemd are required.' >&2; exit 1; }
stack=/opt/remnanode
root=/var/lib/remnamanager-node
if [[ ${RM_NODE_VERSION:-1} == 4 || -f $stack/state/job-id ]]; then
    root=$stack/state
    [[ ! -d $stack || -f $root/job-id ]] || { echo "Existing node directory requires migration." >&2; exit 1; }
fi
table=remnamanager_node
unit=remnamanager-node-firewall
rollback=remnamanager-node-rollback
decode() { printf '%s' "$1" | base64 --decode; }
domain=$(decode "$RM_DOMAIN_B64")
panel_ip=$(decode "$RM_PANEL_IP_B64")
email=$(decode "$RM_EMAIL_B64")
source_image=$(decode "$RM_SOURCE_B64")
api_port=2222
if [[ -n ${RM_API_PORT_B64:-} ]]; then api_port=$(decode "$RM_API_PORT_B64"); fi
[[ $api_port =~ ^[0-9]+$ ]] && ((api_port>0 && api_port<=65535 && api_port!=22 && api_port!=80 && api_port!=443 && api_port!=8443)) || exit 2
node_version=${RM_NODE_VERSION:-1}
# Journal v3 changes panel naming only; remote provisioning uses v2.
[[ $node_version != 3 && $node_version != 4 ]] || node_version=2
[[ $node_version == 1 || $node_version == 2 ]] || exit 2
[[ $domain =~ ^[a-z0-9][a-z0-9.-]+[a-z0-9]$ && $panel_ip =~ ^[a-fA-F0-9:.]+$ ]] || exit 2
if [[ -f $root/job-id ]]; then
    [[ $(cat "$root/job-id") == "$RM_JOB_ID" ]] || { echo 'Another installation owns this server; resume its journal.' >&2; exit 1; }
elif [[ -d $root && -n $(ls -A "$root") ]]; then
    echo 'Existing node agent directory is not owned by this installation.' >&2; exit 1
fi
mkdir -p "$root"
chmod 0700 "$root"
exec 9>"$root/agent.lock"
flock -n 9 || { echo 'Another node operation is running; retry after it completes.' >&2; exit 1; }
exec 3>&1 4>&2
exec >>"$root/install.log" 2>&1
current_action=preflight
action() { current_action=$1; printf 'Node action: %s\n' "$current_action"; }
stage_failure() {
    local exit_status=$?
    trap - ERR
    printf 'RM_NODE_FAILURE=%s\n' "$current_action" >&4
    if [[ ${acme_download:-} == "$root/acme/acme."* && -f ${acme_download:-} ]]; then rm -f -- "$acme_download"; fi
    printf 'Node stage %s failed; remote log: %s/install.log\n' "$RM_STAGE" "$root" >&4
    exit "$exit_status"
}
trap stage_failure ERR
write() {
    local path=$1 mode=${2:-0600} tmp
    [[ ! -e $path && ! -L $path || -f $path && ! -L $path ]] || return 1
    mkdir -p "$(dirname "$path")"
    tmp=$(mktemp "${path}.tmp.XXXXXXXX")
    if cat >"$tmp" && chmod "$mode" "$tmp" && mv -f -- "$tmp" "$path"; then return; fi
    rm -f -- "$tmp"; return 1
}
if [[ ! -f $root/job-id ]]; then printf '%s\n' "$RM_JOB_ID" | write "$root/job-id"; fi
compose() { docker compose --project-directory "$stack" -f "$stack/compose.json" "$@"; }
ssh_ports() {
    local ports port
    ports=$(/usr/sbin/sshd -T | awk '$1=="port" {print $2}')
    if [[ -n ${SSH_CONNECTION:-} ]]; then ports+=$'\n'$(awk '{print $4}' <<<"$SSH_CONNECTION"); fi
    [[ -n $ports ]] || return 1
    for port in $ports; do [[ $port =~ ^[0-9]+$ ]] && ((port>0 && port<=65535 && port!=api_port)) || return 1; done
    printf '%s\n22\n' "$ports" | sort -nu | paste -sd, -
}
check_firewall() {
    if systemctl is-active --quiet firewalld; then echo 'Migrate existing firewalld rules first.' >&2; return 1; fi
    if command -v ufw >/dev/null && ufw status | grep -q 'Status: active'; then echo 'Migrate active UFW first.' >&2; return 1; fi
    local command rules
    for command in iptables-legacy ip6tables-legacy; do
        if command -v "$command" >/dev/null; then rules=$("$command" -S INPUT); [[ $rules == '-P INPUT ACCEPT' ]] || return 1; fi
    done
    if command -v nft >/dev/null; then
        if [[ -f $root/preflight-ok ]] && command -v jq >/dev/null; then
            nft -j list ruleset | jq -e --arg own "$table" '
              [.nftables[] | .chain? | select(.hook=="input" and .table!=$own)] as $chains |
              [.nftables[] | .rule?] as $rules |
              all($chains[]; . as $chain | .policy!="drop" and
                ([$rules[] | select(.family==$chain.family and .table==$chain.table and .chain==$chain.name)]|length)==0)' >/dev/null
        else
            rules=$(nft list ruleset)
            if grep -q 'hook input' <<<"$rules"; then echo 'Migrate existing input firewall rules first.' >&4; return 1; fi
        fi
    fi
}
rules() {
    local full=$1 ports family=ip
    [[ $panel_ip != *:* ]] || family=ip6
    if [[ $full == true ]]; then
        ports=$(ssh_ports)
        cat <<EOF
table inet $table {
 chain input {
  type filter hook input priority -10; policy drop;
  ct state invalid drop
  ct state established,related accept
  iifname "lo" accept
  meta l4proto { 1, 58 } accept
  udp sport 67 udp dport 68 accept
  udp sport 547 udp dport 546 accept
  tcp dport { $ports, 80, 443 } accept
  tcp dport $api_port $family saddr $panel_ip accept
 }
 chain forward {
  type filter hook forward priority -10; policy drop;
  ct state established,related accept
  iifname "docker0" accept
  iifname "br-*" accept
  ct status dnat drop
 }
}
EOF
    else
        cat <<EOF
table inet $table {
 chain input {
  type filter hook input priority -10; policy accept;
  iifname "lo" accept
  tcp dport $api_port $family saddr $panel_ip accept
  tcp dport $api_port drop
 }
}
EOF
    fi
}
apply_file() {
    local file=$1
    if nft list table inet "$table" >/dev/null 2>&1; then
        { printf 'delete table inet %s\n' "$table"; cat "$file"; } | nft -c -f -
        { printf 'delete table inet %s\n' "$table"; cat "$file"; } | nft -f -
    else nft -c -f "$file"; nft -f "$file"; fi
}
case $RM_STAGE in
0)
    # shellcheck disable=SC1091
    . /etc/os-release
    case "$ID:$VERSION_ID" in debian:12|debian:13|ubuntu:22.04|ubuntu:24.04) ;; *) exit 1;; esac
    case $(uname -m) in x86_64|aarch64) ;; *) exit 1;; esac
    [[ ! -d /opt/remnawave ]] || { echo 'Use a separate server: a panel directory already exists.' >&2; exit 1; }
    ssh_ports >/dev/null
    getent ahosts "$domain" >/dev/null
    if [[ ! -f $root/preflight-ok ]]; then
        [[ ! -e $stack/compose.json && ! -e /etc/nginx && ! -e /etc/caddy && ! -e /etc/apache2 && ! -e /etc/systemd/system/$unit.service ]] || { echo 'Existing node/proxy configuration requires migration.' >&4; exit 1; }
        command -v ss >/dev/null
        listeners=$(ss -H -ltn)
        if awk '{print $4}' <<<"$listeners" | grep -Eq ":(80|443|8443|$api_port)$"; then echo 'Reserved node ports are already in use.' >&4; exit 1; fi
    fi
    check_firewall
    printf '%s\n' "$RM_JOB_ID" | write "$root/preflight-ok"
    ;;
1)
    action dependencies
    [[ -f $root/preflight-ok ]]
    arch=amd64; [[ $(uname -m) != aarch64 ]] || arch=arm64
    setup_proxy=nginx; [[ $node_version != 2 ]] || setup_proxy=node
    decode "$RM_SETUP_B64" | bash -s -- "$setup_proxy" "$arch"
    if ! command -v jq >/dev/null; then apt-get update; DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends jq; fi
    ;;
2)
    action image-download
    [[ -f $root/preflight-ok ]]
    [[ $source_image == remnawave/node:latest || $source_image =~ ^remnawave/node@sha256:[a-f0-9]{64}$ ]]
    docker pull "$source_image"
    image=$(docker image inspect --format '{{index .RepoDigests 0}}' "$source_image")
    image=${image#docker.io/}
    [[ $image =~ ^remnawave/node@sha256:[a-f0-9]{64}$ ]]
    printf 'RM_NODE_IMAGE=%s\n' "$image" >&3
    ;;
3)
    action firewall
    check_firewall
    [[ ! -e $stack || -f $stack/.remnamanager-go-node || ${RM_NODE_VERSION:-1} == 4 && -f $root/preflight-ok ]]
    mkdir -p "$stack"; chmod 0755 "$stack"
    printf '%s\n' "$RM_JOB_ID" | write "$stack/.remnamanager-go-node"
    decode "$RM_COMPOSE_B64" | write "$stack/compose.json"
    action compose-config
    compose config --quiet
    # Restrict the node API before the host-network container can start.
    if [[ ! -f $root/firewall-active.nft ]]; then rules false | write "$root/firewall-active.nft"; fi
    { printf '#!/bin/sh\nRM_FIREWALL_FILE=%q\n' "$root/firewall-active.nft"; cat <<'EOF'
set -eu
table=remnamanager_node
file=$RM_FIREWALL_FILE
if /usr/sbin/nft list table inet "$table" >/dev/null 2>&1; then
 { echo "delete table inet $table"; cat "$file"; } | /usr/sbin/nft -f -
else /usr/sbin/nft -f "$file"; fi
EOF
    } | write "$root/firewall-apply.sh" 0700
    cat <<EOF | write "/etc/systemd/system/$unit.service" 0644
[Unit]
Description=RemnaManager node firewall
After=network-pre.target
Before=docker.service
[Service]
Type=oneshot
RemainAfterExit=yes
ExecStart=$root/firewall-apply.sh
ExecReload=$root/firewall-apply.sh
[Install]
WantedBy=multi-user.target
EOF
    action firewall
    apply_file "$root/firewall-active.nft"
    systemctl daemon-reload; systemctl enable "$unit.service"
    action site-files
    web=/var/www/remnamanager-node
    if [[ $root == "$stack/state" ]]; then web=$stack/www; fi
    install -d -m 0755 "$web" "$web/site" "$web/acme" "$web/acme/.well-known" "$web/acme/.well-known/acme-challenge"
    if [[ $node_version == 2 ]]; then
        [[ -n ${RM_SITE_B64:-} ]]
        (umask 022; decode "$RM_SITE_B64" | tar -xzf - -C "$web/site" --no-same-owner --no-same-permissions)
    elif [[ ! -f $web/site/index.html ]]; then
        cat <<'EOF' | write "$web/site/index.html" 0644
<!doctype html><html lang="en"><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><title>North Studio</title><style>body{margin:0;background:#e7e0d5;color:#252828;font:18px system-ui}main{max-width:800px;margin:15vh auto;padding:32px}h1{font-size:clamp(40px,8vw,80px);letter-spacing:-.04em}p{line-height:1.7}</style><main><small>Independent creative studio</small><h1>North Studio</h1><p>Thoughtful design. Lasting impressions.</p><p>We create considered digital experiences, one detail at a time.</p></main></html>
EOF
    fi
    if [[ -L /etc/nginx/sites-enabled/default && $(readlink -f /etc/nginx/sites-enabled/default) == /etc/nginx/sites-available/default ]]; then rm /etc/nginx/sites-enabled/default; fi
    conf=/etc/nginx/conf.d/remnamanager-node.conf
    if [[ $root == "$stack/state" ]]; then
        conf=$stack/nginx/node.conf
        mkdir -p "$stack/nginx"
        ln -sfn "$conf" /etc/nginx/conf.d/remnamanager-node.conf
    fi
    [[ ! -f $conf ]] || cp "$conf" "$root/nginx.previous"
    cat <<EOF | write "$conf" 0644
# Managed by RemnaManager node installer
server {
 listen 80; listen [::]:80; server_name $domain;
 location ^~ /.well-known/acme-challenge/ { root $web/acme; }
 location / { return 503; }
}
EOF
    action nginx-config
    nginx -t
    action nginx-start
    systemctl enable --now nginx; systemctl reload nginx
    fullchain=/etc/letsencrypt/live/$domain/fullchain.pem
    private_key=/etc/letsencrypt/live/$domain/privkey.pem
    proxy_protocol=''
    real_ip=''
    if [[ $node_version == 2 ]]; then
        acme_home=$root/acme
        mkdir -p "$acme_home" "$root/ssl"; chmod 0700 "$acme_home" "$root/ssl"
        if [[ ! -f $acme_home/acme.sh ]]; then
            action acme-download
            acme_download=$(mktemp "$acme_home/acme.XXXXXXXX")
            curl -fsSL --proto '=https' --proto-redir '=https' --connect-timeout 15 --max-time 120 --retry 3 --retry-delay 2 https://raw.githubusercontent.com/acmesh-official/acme.sh/807da6498377ee5e0cf43a78091f46f12dc59a89/acme.sh -o "$acme_download"
            action acme-checksum
            printf '%s  %s\n' c7d68b021cfd6380ea83a82962abde5b484779fee0b97d38681dfa1396bbc8d7 "$acme_download" | sha256sum -c -
            write "$acme_home/acme.sh" 0700 <"$acme_download"
            rm -f -- "$acme_download"
        fi
        action acme-checksum
        printf '%s  %s\n' c7d68b021cfd6380ea83a82962abde5b484779fee0b97d38681dfa1396bbc8d7 "$acme_home/acme.sh" | sha256sum -c -
        acme() { sh "$acme_home/acme.sh" --home "$acme_home" --config-home "$acme_home" --server letsencrypt "$@"; }
        action acme-account
        acme --register-account -m "$email"
        issue_status=0
        action acme-issue
        acme --issue --webroot "$web/acme" --keylength ec-256 -d "$domain" || issue_status=$?
        [[ $issue_status == 0 || $issue_status == 2 ]]
        printf '%s\n' '#!/bin/sh' 'set -eu' '/usr/sbin/nginx -t' '/bin/systemctl reload nginx' | write "$root/reload-nginx.sh" 0700
        fullchain=$root/ssl/fullchain.pem
        private_key=$root/ssl/privkey.pem
        action acme-install
        acme --install-cert --ecc -d "$domain" --fullchain-file "$fullchain" --key-file "$private_key" --reloadcmd "$root/reload-nginx.sh"
        cat <<EOF | write /etc/systemd/system/remnamanager-node-acme.service 0644
[Unit]
Description=RemnaManager node certificate renewal
After=network-online.target nginx.service
[Service]
Type=oneshot
UMask=0077
ExecStart=/bin/sh $acme_home/acme.sh --cron --home $acme_home --config-home $acme_home
EOF
        cat <<'EOF' | write /etc/systemd/system/remnamanager-node-acme.timer 0644
[Unit]
Description=Daily ACME renewal check for the node
[Timer]
OnCalendar=daily
RandomizedDelaySec=6h
Persistent=true
[Install]
WantedBy=timers.target
EOF
        action acme-timer
        systemctl daemon-reload
        systemctl enable --now remnamanager-node-acme.timer
        proxy_protocol=' proxy_protocol'
        real_ip='set_real_ip_from 127.0.0.1; real_ip_header proxy_protocol;'
    else
        certbot certonly --webroot -w "$web/acme" -d "$domain" --email "$email" --agree-tos --non-interactive --keep-until-expiring
    fi
    cat <<EOF | write "$conf" 0644
# Managed by RemnaManager node installer
server {
 listen 80; listen [::]:80; server_name $domain;
 location ^~ /.well-known/acme-challenge/ { root $web/acme; }
 location / { return 301 https://\$host\$request_uri; }
}
server {
 listen 127.0.0.1:8443 ssl http2$proxy_protocol; server_name $domain;
 $real_ip
 ssl_certificate $fullchain;
 ssl_certificate_key $private_key;
 ssl_protocols TLSv1.2 TLSv1.3; server_tokens off;
 root $web/site;
 error_page 404 /404.html;
 location / { try_files \$uri \$uri/ =404; }
}
EOF
    action nginx-https
    if ! nginx -t || ! systemctl reload nginx; then
        if [[ -f $root/nginx.previous ]]; then write "$conf" 0644 <"$root/nginx.previous"; nginx -t && systemctl reload nginx; fi
        false
    fi
    if [[ $node_version == 1 ]]; then
        printf '%s\n' '#!/bin/sh' 'set -eu' '/usr/sbin/nginx -t' '/bin/systemctl reload nginx' | write /etc/letsencrypt/renewal-hooks/deploy/remnamanager-node 0700
        systemctl enable --now certbot.timer
    fi
    action node-start
    compose up -d --wait --wait-timeout 120
    ;;
7)
    action firewall-trial
    check_firewall
    [[ -f $stack/.remnamanager-go-node && -f $root/firewall-active.nft ]]
    if [[ -f $root/firewall-pending ]] && systemctl is-active --quiet "$rollback.timer"; then
        echo 'Firewall trial is already pending; confirm before its original deadline.' >&3
    else
        rules true | write "$root/firewall-trial.nft"
        nft list table inet "$table" >"$root/firewall-before-trial.nft"
        { echo "delete table inet $table"; cat "$root/firewall-before-trial.nft"; } | write "$root/firewall-rollback.nft"
        write "$root/firewall-active-before-trial.nft" <"$root/firewall-active.nft"
        { echo "delete table inet $table"; cat "$root/firewall-trial.nft"; } | nft -c -f -
        systemctl stop "$rollback.timer" "$rollback.service" || true
        systemctl reset-failed "$rollback.service" || true
        systemd-run --collect --unit="$rollback" --on-active=120s --timer-property=AccuracySec=1s /usr/sbin/nft -f "$root/firewall-rollback.nft"
        printf '%s\n' "$(( $(date +%s) + 120 ))" | write "$root/firewall-pending"
        apply_file "$root/firewall-trial.nft"
    fi
    printf 'RM_FW_REMAINING=%s\n' "$(( $(cat "$root/firewall-pending") - $(date +%s) ))" >&3
    ;;
8)
    action firewall-confirm
    if [[ -f $root/ready && ! -f $root/firewall-pending ]]; then
        nft list table inet "$table" >/dev/null
    else
        [[ -f $root/firewall-pending ]]
        deadline=$(cat "$root/firewall-pending")
        [[ $deadline =~ ^[0-9]+$ ]] && (( $(date +%s) < deadline - 10 ))
        systemctl is-active --quiet "$rollback.timer"
        nft list table inet "$table" >/dev/null
        write "$root/firewall-active.nft" <"$root/firewall-trial.nft"
        if ! systemctl stop "$rollback.timer"; then write "$root/firewall-active.nft" <"$root/firewall-active-before-trial.nft"; exit 1; fi
        printf '%s\n' "$RM_JOB_ID" | write "$root/ready"
        rm -f "$root/firewall-pending"
    fi
    ;;
*) exit 2;;
esac
printf 'Node stage %s complete.\n' "$RM_STAGE" >&3
