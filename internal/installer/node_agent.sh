set -Eeuo pipefail
umask 077
[[ ${RM_JOB_ID:-} =~ ^[a-f0-9]{32}$ && ${RM_STAGE:-} =~ ^[0-8]$ ]] || exit 2
[[ $(id -u) == 0 && -f /etc/os-release && -d /run/systemd/system ]] || { echo 'Linux root and systemd are required.' >&2; exit 1; }
root=/var/lib/remnamanager-node
stack=/opt/remnanode
table=remnamanager_node
unit=remnamanager-node-firewall
rollback=remnamanager-node-rollback
decode() { printf '%s' "$1" | base64 --decode; }
domain=$(decode "$RM_DOMAIN_B64")
panel_ip=$(decode "$RM_PANEL_IP_B64")
email=$(decode "$RM_EMAIL_B64")
source_image=$(decode "$RM_SOURCE_B64")
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
stage_failure() {
    local exit_status=$?
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
    for port in $ports; do [[ $port =~ ^[0-9]+$ ]] && ((port>0 && port<=65535 && port!=2222)) || return 1; done
    sort -nu <<<"$ports" | paste -sd, -
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
  tcp dport 2222 $family saddr $panel_ip accept
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
  tcp dport 2222 $family saddr $panel_ip accept
  tcp dport 2222 drop
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
        [[ ! -e $stack && ! -e /etc/nginx && ! -e /etc/caddy && ! -e /etc/apache2 && ! -e /etc/systemd/system/$unit.service ]] || { echo 'Existing node/proxy configuration requires migration.' >&4; exit 1; }
        command -v ss >/dev/null
        listeners=$(ss -H -ltn)
        if awk '{print $4}' <<<"$listeners" | grep -Eq ':(80|443|8443|2222)$'; then echo 'Reserved node ports are already in use.' >&4; exit 1; fi
    fi
    check_firewall
    printf '%s\n' "$RM_JOB_ID" | write "$root/preflight-ok"
    ;;
1)
    [[ -f $root/preflight-ok ]]
    arch=amd64; [[ $(uname -m) != aarch64 ]] || arch=arm64
    decode "$RM_SETUP_B64" | bash -s -- nginx "$arch"
    if ! command -v jq >/dev/null; then apt-get update; DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends jq; fi
    ;;
2)
    [[ -f $root/preflight-ok ]]
    [[ $source_image == remnawave/node:latest || $source_image =~ ^remnawave/node@sha256:[a-f0-9]{64}$ ]]
    docker pull "$source_image"
    image=$(docker image inspect --format '{{index .RepoDigests 0}}' "$source_image")
    image=${image#docker.io/}
    [[ $image =~ ^remnawave/node@sha256:[a-f0-9]{64}$ ]]
    printf 'RM_NODE_IMAGE=%s\n' "$image" >&3
    ;;
3)
    check_firewall
    [[ ! -e $stack || -f $stack/.remnamanager-go-node ]]
    mkdir -p "$stack"; chmod 0700 "$stack"
    printf '%s\n' "$RM_JOB_ID" | write "$stack/.remnamanager-go-node"
    decode "$RM_COMPOSE_B64" | write "$stack/compose.json"
    compose config --quiet
    # Restrict the node API before the host-network container can start.
    if [[ ! -f $root/firewall-active.nft ]]; then rules false | write "$root/firewall-active.nft"; fi
    cat <<'EOF' | write "$root/firewall-apply.sh" 0700
#!/bin/sh
set -eu
table=remnamanager_node
file=/var/lib/remnamanager-node/firewall-active.nft
if /usr/sbin/nft list table inet "$table" >/dev/null 2>&1; then
 { echo "delete table inet $table"; cat "$file"; } | /usr/sbin/nft -f -
else /usr/sbin/nft -f "$file"; fi
EOF
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
    apply_file "$root/firewall-active.nft"
    systemctl daemon-reload; systemctl enable "$unit.service"
    web=/var/www/remnamanager-node
    install -d -m 0755 "$web" "$web/site" "$web/acme" "$web/acme/.well-known" "$web/acme/.well-known/acme-challenge"
    if [[ ! -f $web/site/index.html ]]; then
        cat <<'EOF' | write "$web/site/index.html" 0644
<!doctype html><html lang="en"><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><title>North Studio</title><style>body{margin:0;background:#e7e0d5;color:#252828;font:18px system-ui}main{max-width:800px;margin:15vh auto;padding:32px}h1{font-size:clamp(40px,8vw,80px);letter-spacing:-.04em}p{line-height:1.7}</style><main><small>Independent creative studio</small><h1>North Studio</h1><p>Thoughtful design. Lasting impressions.</p><p>We create considered digital experiences, one detail at a time.</p></main></html>
EOF
    fi
    if [[ -L /etc/nginx/sites-enabled/default && $(readlink -f /etc/nginx/sites-enabled/default) == /etc/nginx/sites-available/default ]]; then rm /etc/nginx/sites-enabled/default; fi
    conf=/etc/nginx/conf.d/remnamanager-node.conf
    [[ ! -f $conf ]] || cp "$conf" "$root/nginx.previous"
    cat <<EOF | write "$conf" 0644
# Managed by RemnaManager node installer
server {
 listen 80; listen [::]:80; server_name $domain;
 location ^~ /.well-known/acme-challenge/ { root $web/acme; }
 location / { return 503; }
}
EOF
    nginx -t; systemctl enable --now nginx; systemctl reload nginx
    certbot certonly --webroot -w "$web/acme" -d "$domain" --email "$email" --agree-tos --non-interactive --keep-until-expiring
    cat <<EOF | write "$conf" 0644
# Managed by RemnaManager node installer
server {
 listen 80; listen [::]:80; server_name $domain;
 location ^~ /.well-known/acme-challenge/ { root $web/acme; }
 location / { return 301 https://\$host\$request_uri; }
}
server {
 listen 127.0.0.1:8443 ssl; server_name $domain;
 ssl_certificate /etc/letsencrypt/live/$domain/fullchain.pem;
 ssl_certificate_key /etc/letsencrypt/live/$domain/privkey.pem;
 ssl_protocols TLSv1.2 TLSv1.3; server_tokens off;
 root $web/site;
 location / { try_files \$uri \$uri/ =404; }
}
EOF
    if ! nginx -t || ! systemctl reload nginx; then
        if [[ -f $root/nginx.previous ]]; then write "$conf" 0644 <"$root/nginx.previous"; nginx -t && systemctl reload nginx; fi
        exit 1
    fi
    printf '%s\n' '#!/bin/sh' 'set -eu' '/usr/sbin/nginx -t' '/bin/systemctl reload nginx' | write /etc/letsencrypt/renewal-hooks/deploy/remnamanager-node 0700
    systemctl enable --now certbot.timer
    compose up -d --wait --wait-timeout 120
    ;;
7)
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
