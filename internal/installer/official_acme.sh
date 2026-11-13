set -Eeuo pipefail
umask 077
manager=$1
stack=$2
email=$3
ca=$4
domains=$5
method=${RM_CERT_METHOD:-acme}
case "$method" in acme|cloudflare|gcore|bunny) ;; *) exit 2;; esac
case "$ca" in letsencrypt|zerossl) ;; *) exit 2;; esac
mkdir -p "$manager" "$stack/nginx"
acme="$manager/acme/acme.sh"
if [[ ! -x $acme ]]; then
    installer=$(mktemp)
    trap 'rm -f "$installer"' EXIT
    curl -fsSL --proto '=https' --proto-redir '=https' --connect-timeout 15 --max-time 120 https://get.acme.sh -o "$installer"
    # Installation can report an account error; retry registration explicitly
    # only if the client itself was successfully installed.
    # get.acme.sh converts its first email=value argument into --email value.
    sh "$installer" "email=$email" --home "$manager/acme" --config-home "$manager/acme" || true
    [[ -x $acme ]] || { echo 'acme.sh installation failed' >&2; exit 1; }
    rm -f "$installer"
fi
register_account() {
    local server account
    "$acme" --register-account --home "$manager/acme" -m "$email" --server "$ca" || return 1
    server=acme.zerossl.com
    [[ $ca != letsencrypt ]] || server=acme-v02.api.letsencrypt.org
    account="$manager/acme/ca/$server/v2/DV90/ca.conf"
    [[ $ca != letsencrypt ]] || account="$manager/acme/ca/$server/directory/ca.conf"
    grep -Eq '^ACCOUNT_URL=.+https?://' "$account"
}
if ! register_account; then
    [[ $ca == zerossl ]] || { echo 'ACME registration failed; no certificate requested.' >&2; exit 1; }
    echo 'ZeroSSL registration failed or account URL missing; switching to Lets Encrypt.'
    ca=letsencrypt
    "$acme" --set-default-ca --home "$manager/acme" --server letsencrypt
    register_account || { echo 'Lets Encrypt registration failed; no certificate requested.' >&2; exit 1; }
fi

# DNS credentials arrive on stdin as shell data prepared by Go. Encrypt at rest;
# acme.sh DNS hooks must not save a second unencrypted copy in account.conf.
if [[ $method != acme ]]; then
    case "$method" in
        cloudflare) dns_hook=dns_cf; token_name=CF_Token;;
        gcore) dns_hook=dns_gcore; token_name=GCORE_Key;;
        bunny) dns_hook=dns_bunny; token_name=BUNNY_API_KEY;;
    esac
    [[ -f $manager/acme/dnsapi/$dns_hook.sh ]] || { echo 'installed acme.sh does not support selected DNS API' >&2; exit 1; }
    sed -i -E "/^[[:space:]]*(_saveaccountconf_mutable|_savedomainconf) $token_name /d" "$manager/acme/dnsapi/$dns_hook.sh"
    [[ -s $manager/dns-token.key ]] || openssl rand -hex 32 > "$manager/dns-token.key"
    printf '%s' "$RM_DNS_TOKEN" | openssl enc -aes-256-cbc -pbkdf2 -salt -pass "file:$manager/dns-token.key" -out "$manager/dns-token.enc"
    export "$token_name=$RM_DNS_TOKEN"
    unset RM_DNS_TOKEN
    printf '#!/usr/bin/env bash\nset -Eeuo pipefail\numask 077\nmanager=%q\ntoken_name=%q\n' "$manager" "$token_name" > "$manager/renew-dns.sh"
    cat >> "$manager/renew-dns.sh" <<'DNSRENEW'
token=$(openssl enc -d -aes-256-cbc -pbkdf2 -pass "file:$manager/dns-token.key" -in "$manager/dns-token.enc")
export "$token_name=$token"
unset token
exec "$manager/acme/acme.sh" --cron --home "$manager/acme" --config-home "$manager/acme"
DNSRENEW
    chmod 700 "$manager/renew-dns.sh"
fi

# TLS-ALPN-01 always arrives on public 443. Relay it to the requested local
# 8443 listener while the owned Nginx is stopped, including future renewals.
relay="$manager/acme-relay.sh"
printf '#!/usr/bin/env bash\nset -Eeuo pipefail\nmanager=%q\nstack=%q\n' "$manager" "$stack" > "$relay"
cat >> "$relay" <<'RELAY'
case "$1" in
 start)
    if docker inspect remnawave-nginx >/dev/null 2>&1; then
        owner=$(docker inspect --format '{{index .Config.Labels "com.docker.compose.project.working_dir"}}' remnawave-nginx)
        [[ $owner == "$stack/nginx" ]] || { echo 'Nginx container belongs to another installation' >&2; exit 1; }
        if [[ $(docker inspect --format '{{.State.Running}}' remnawave-nginx) == true ]]; then
            printf 'running\n' > "$manager/acme-nginx.was-running"
            docker stop remnawave-nginx >/dev/null
        fi
    fi
    if docker inspect caddy >/dev/null 2>&1; then
        owner=$(docker inspect --format '{{index .Config.Labels "com.docker.compose.project.working_dir"}}' caddy)
        if [[ $owner == "$stack/caddy" && $(docker inspect --format '{{.State.Running}}' caddy) == true ]]; then
            printf 'running\n' > "$manager/acme-caddy.was-running"
            docker stop caddy >/dev/null
        fi
    fi
    if ss -H -ltn | awk '{print $4}' | grep -Eq ':(443|8443)$'; then echo '443 or 8443 is busy; no unrelated service will be stopped.' >&2; exit 1; fi
    socat TCP4-LISTEN:443,reuseaddr,fork TCP4:127.0.0.1:8443 >> "$manager/acme-relay.log" 2>&1 &
    echo "$!" > "$manager/acme-relay-v4.pid"
    if [[ -s /proc/net/if_inet6 ]]; then
        socat TCP6-LISTEN:443,ipv6only=1,reuseaddr,fork TCP4:127.0.0.1:8443 >> "$manager/acme-relay.log" 2>&1 &
        echo "$!" > "$manager/acme-relay-v6.pid"
    fi
    sleep 1
    kill -0 "$(cat "$manager/acme-relay-v4.pid")"
    ;;
 finish)
    for version in v4 v6; do
        file="$manager/acme-relay-$version.pid"
        if [[ -f $file ]]; then
            pid=$(cat "$file")
            if [[ $pid =~ ^[0-9]+$ && -r /proc/$pid/comm && $(cat /proc/"$pid"/comm) == socat ]]; then kill "$pid" 2>/dev/null || true; fi
            rm -f "$file"
        fi
    done
    if [[ -f $manager/acme-nginx.was-running ]]; then docker start remnawave-nginx >/dev/null; rm -f "$manager/acme-nginx.was-running"; fi
    if [[ -f $manager/acme-caddy.was-running ]]; then docker start caddy >/dev/null; rm -f "$manager/acme-caddy.was-running"; fi
    ;;
esac
RELAY
chmod 700 "$relay"
printf '#!/usr/bin/env bash\nset -Eeuo pipefail\n%q finish\nstack=%q\n' "$relay" "$stack" > "$manager/reload-nginx.sh"
cat >> "$manager/reload-nginx.sh" <<'RELOAD'
if [[ $(docker inspect --format '{{.State.Running}}' remnawave-nginx 2>/dev/null || true) == true ]]; then
    docker exec remnawave-nginx nginx -t
    docker exec remnawave-nginx nginx -s reload
elif [[ $(docker inspect --format '{{index .Config.Labels "com.docker.compose.project.working_dir"}}' caddy 2>/dev/null || true) == "$stack/caddy" ]]; then
    docker exec caddy caddy validate --config /etc/caddy/Caddyfile --adapter caddyfile
    docker exec caddy caddy reload --force --config /etc/caddy/Caddyfile --adapter caddyfile
fi
RELOAD
chmod 700 "$manager/reload-nginx.sh"
IFS=, read -r -a names <<< "$domains"
[[ ${#names[@]} -gt 0 ]] || exit 2
args=()
for domain in "${names[@]}"; do [[ $domain =~ ^[a-zA-Z0-9.-]+$ ]] || exit 2; args+=(-d "$domain"); done
trap '"$relay" finish' EXIT
issue_status=0
# --alpn already selects standalone TLS mode. Adding --standalone would assign
# HTTP-01 to the first domain and ALPN to the next domain instead.
if [[ $method == acme ]]; then
    "$acme" --issue --home "$manager/acme" --server "$ca" "${args[@]}" --alpn --tlsport 8443 --keylength ec-256 --pre-hook "'$relay' start" --post-hook "'$relay' finish" || issue_status=$?
else
    # Keep exact SAN names as well as wildcards: apex and deeper labels are
    # never assumed to be covered by a wildcard of a different level.
    declare -A wildcard_seen=()
    for domain in "${names[@]}"; do
        # Pair each exact name with a wildcard below that name. Never guess
        # its registrable zone (e.g. example.co.uk must not become *.co.uk).
        parent=$domain
        if [[ -z ${wildcard_seen[$parent]:-} ]]; then args+=(-d "*.$parent"); wildcard_seen[$parent]=1; fi
    done
    "$acme" --issue --home "$manager/acme" --server "$ca" "${args[@]}" --dns "$dns_hook" --keylength ec-256 || issue_status=$?
fi
[[ $issue_status == 0 || $issue_status == 2 ]] || exit "$issue_status"
"$relay" finish
"$acme" --install-cert --home "$manager/acme" --ecc -d "${names[0]}" --key-file "$stack/nginx/privkey.key" --fullchain-file "$stack/nginx/fullchain.pem" --reloadcmd "$manager/reload-nginx.sh"
