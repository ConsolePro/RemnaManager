set -Eeuo pipefail
umask 077
manager=$1
stack=$2
email=$3
ca=$4
domains=$5
case "$ca" in letsencrypt|zerossl) ;; *) exit 2;; esac
acme="$manager/acme/acme.sh"
if [[ ! -x $acme ]]; then
    installer=$(mktemp)
    trap 'rm -f "$installer"' EXIT
    curl -fsSL --proto '=https' --proto-redir '=https' --connect-timeout 15 --max-time 120 https://get.acme.sh -o "$installer"
    # get.acme.sh converts its first email=value argument into --email value.
    sh "$installer" "email=$email" --home "$manager/acme" --config-home "$manager/acme"
    [[ -x $acme ]] || { echo 'acme.sh installation failed' >&2; exit 1; }
fi
"$acme" --register-account --home "$manager/acme" --server "$ca" -m "$email"
# HTTP-01 uses public TCP 80; no unsupported 8443/ALPN forwarding assumption.
IFS=, read -r -a names <<< "$domains"
for domain in "${names[@]}"; do
    [[ $domain =~ ^[a-zA-Z0-9.-]+$ ]] || exit 2
    certdir="$stack/nginx/certs/$domain"
    mkdir -p "$certdir"
    issue_status=0
    "$acme" --issue --home "$manager/acme" --server "$ca" --webroot "$stack/nginx/acme" -d "$domain" --keylength ec-256 || issue_status=$?
    [[ $issue_status == 0 || $issue_status == 2 ]] || exit "$issue_status"
    hook="$manager/reload-nginx.sh"
    printf '#!/usr/bin/env bash\nset -Eeuo pipefail\nstack=%q\n' "$stack" > "$hook"
    cat >> "$hook" <<'HOOK'
nginx_id=$(docker compose --project-directory "$stack/nginx" -f "$stack/nginx/docker-compose.yml" ps -q nginx 2>/dev/null || true)
if [[ -n $nginx_id ]]; then
    docker compose --project-directory "$stack/nginx" -f "$stack/nginx/docker-compose.yml" exec -T nginx nginx -t
    docker compose --project-directory "$stack/nginx" -f "$stack/nginx/docker-compose.yml" exec -T nginx nginx -s reload
else
    docker compose --project-directory "$stack/caddy" -f "$stack/caddy/docker-compose.yml" exec -T caddy caddy reload --force --config /etc/caddy/Caddyfile --adapter caddyfile
fi
HOOK
    chmod 700 "$hook"
    "$acme" --install-cert --home "$manager/acme" --ecc -d "$domain" --key-file "$certdir/privkey.pem" --fullchain-file "$certdir/fullchain.pem" --reloadcmd "$hook"
done
