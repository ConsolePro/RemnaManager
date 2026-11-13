set -Eeuo pipefail
umask 077
decode() { printf '%s' "$1" | base64 -d; }
action=$(decode "$RM_ACTION_B64")
proxy=$(decode "$RM_PROXY_B64")
domain=$(decode "$RM_DOMAIN_B64")
layout=$(decode "${RM_LAYOUT_B64:-MA==}")
subservice=subscription
proxyservice=$proxy
if [[ $layout -ge 2 ]]; then
    subservice=remnawave-subscription-page
    [[ $proxy != nginx ]] || proxyservice=remnawave-nginx
fi
stack=/opt/remnawave
manager=/var/lib/remnamanager-subscription
[[ $(id -u) == 0 ]] || { echo 'root or passwordless sudo required' >&2; exit 10; }
case "$proxy" in nginx|caddy) ;; *) exit 2;; esac
mkdir -p "$manager"
exec 9>"$manager/operation.lock"
flock -n 9 || { echo 'another remote subscription operation is running' >&2; exit 11; }
compose() { docker compose --project-directory "$stack/subscription" -f "$stack/subscription/docker-compose.yml" "$@"; }
proxy_compose() { docker compose --project-directory "$stack/$proxy" -f "$stack/$proxy/docker-compose.yml" "$@"; }
owned() { [[ -f $stack/.remnamanager-subscription && $(cat "$stack/.remnamanager-subscription") == managed ]] || { echo 'remote stack is not owned by this installation' >&2; exit 12; }; }
healthy() {
    local id status
    for attempt in {1..60}; do
        id=$(compose ps -q "$subservice")
        if [[ -n $id ]]; then
            status=$(docker inspect --format '{{if .State.Health}}{{.State.Health.Status}}{{else}}missing-healthcheck{{end}}' "$id")
            [[ $status == healthy ]] && return 0
            [[ $status != missing-healthcheck ]] || { echo 'subscription image has no HEALTHCHECK' >&2; return 1; }
        fi
        sleep 1
    done
    echo 'subscription readiness timeout' >&2
    return 1
}
case "$action" in
    preflight)
        . /etc/os-release
        case "$ID:$VERSION_ID" in debian:12|debian:13|ubuntu:22.04|ubuntu:24.04) ;; *) echo 'unsupported remote OS' >&2; exit 13;; esac
        if [[ ! -f $stack/.remnamanager-subscription ]]; then
            [[ ! -d $stack || -z $(find "$stack" -mindepth 1 -maxdepth 1 -print -quit) ]] || { echo 'existing remote stack is not managed' >&2; exit 12; }
            if ss -H -ltn | awk '{print $4}' | grep -Eq ':(80|443|3010)$'; then echo 'remote port conflict' >&2; exit 14; fi
        fi
        getent ahosts "$domain" >/dev/null || { echo 'subscription DNS is not ready' >&2; exit 15; }
        if command -v docker >/dev/null; then docker info >/dev/null; docker compose version; else echo 'Docker will be installed after confirmation'; fi
        ;;
    install)
        . /etc/os-release
        case "$ID:$VERSION_ID" in debian:12|debian:13|ubuntu:22.04|ubuntu:24.04) ;; *) echo 'unsupported remote OS' >&2; exit 13;; esac
        [[ -d /run/systemd/system ]] || { echo 'systemd required' >&2; exit 13; }
        if [[ ! -f $stack/.remnamanager-subscription ]]; then
            [[ ! -d $stack || -z $(find "$stack" -mindepth 1 -maxdepth 1 -print -quit) ]] || { echo 'existing remote installation will not be overwritten' >&2; exit 12; }
            if ss -H -ltn | awk '{print $4}' | grep -Eq ':(80|443|3010)$'; then echo 'remote port conflict' >&2; exit 14; fi
        else owned; fi
        getent ahosts "$domain" >/dev/null || { echo 'subscription DNS is not ready' >&2; exit 15; }
        decode "$RM_SETUP_B64" > "$manager/setup.sh"
        architecture=$(uname -m)
        case "$architecture" in x86_64) architecture=amd64;; aarch64) architecture=arm64;; *) exit 13;; esac
        bash "$manager/setup.sh" docker-proxy "$architecture"
        mkdir -p "$stack/subscription" "$stack/$proxy" "$stack/nginx/certs" "$stack/nginx/acme/.well-known/acme-challenge" "$stack/caddy/data" "$stack/caddy/config"
        chmod 755 "$stack/nginx/acme" "$stack/nginx/acme/.well-known" "$stack/nginx/acme/.well-known/acme-challenge"
        printf 'managed\n' > "$stack/.remnamanager-subscription"
        # Never replace a working subscription image during a retry of install.
        [[ -f $stack/subscription/docker-compose.yml ]] || decode "$RM_COMPOSE_B64" > "$stack/subscription/docker-compose.yml"
        decode "$RM_ENV_B64" > "$stack/subscription/.env"
        compose config --quiet
        compose pull "$subservice"
        compose up -d --wait --wait-timeout 120 "$subservice"
        healthy
        if [[ $layout -ge 2 ]]; then
            decode "$RM_PROXY_COMPOSE_B64" > "$stack/$proxy/docker-compose.yml"
            if [[ $proxy == nginx ]]; then
                if ! openssl x509 -in "$stack/nginx/fullchain.pem" -noout -checkend 0 -checkhost "$domain" >/dev/null 2>&1; then
                    decode "$RM_ACME_B64" > "$manager/acme-setup.sh"
                    bash "$manager/acme-setup.sh" "$manager" "$stack" "$(decode "$RM_EMAIL_B64")" "$(decode "$RM_CA_B64")" "$domain"
                fi
                decode "$RM_CONFIG_B64" > "$stack/nginx/nginx.conf"
                proxy_compose run --rm --no-deps "$proxyservice" nginx -t
            else
                decode "$RM_CONFIG_B64" > "$stack/caddy/Caddyfile"
                proxy_compose run --rm --no-deps caddy caddy validate --config /etc/caddy/Caddyfile --adapter caddyfile
            fi
            proxy_compose up -d
            if [[ $proxy == nginx ]]; then proxy_compose exec -T "$proxyservice" nginx -s reload; else proxy_compose exec -T caddy caddy reload --config /etc/caddy/Caddyfile --adapter caddyfile; fi
        elif [[ $proxy == caddy ]]; then
            decode "$RM_CONFIG_B64" > "$stack/caddy/Caddyfile"
            cat > "$stack/caddy/docker-compose.yml" <<'COMPOSE'
name: remnamanager-subscription-caddy
services:
  caddy:
    image: caddy:2-alpine
    restart: unless-stopped
    ports: ["80:80", "443:443", "443:443/udp"]
    volumes: ["./:/etc/caddy:ro", "./data:/data", "./config:/config"]
    networks: [panel]
networks:
  panel:
    external: true
    name: remnamanager-subscription_panel
COMPOSE
            proxy_compose run --rm --no-deps caddy caddy validate --config /etc/caddy/Caddyfile --adapter caddyfile
            proxy_compose up -d
            proxy_compose exec -T caddy caddy reload --config /etc/caddy/Caddyfile --adapter caddyfile
        else
            cat > "$stack/nginx/docker-compose.yml" <<'COMPOSE'
name: remnamanager-subscription-nginx
services:
  nginx:
    image: nginx:1.30-alpine
    restart: unless-stopped
    ports: ["80:80", "443:443"]
    volumes: ["./:/etc/nginx/conf.d:ro", "./certs:/etc/nginx/certs:ro", "./acme:/var/www/remnamanager-acme:ro"]
    networks: [panel]
networks:
  panel:
    external: true
    name: remnamanager-subscription_panel
COMPOSE
            certdir="$stack/nginx/certs/$domain"
            if ! openssl x509 -in "$certdir/fullchain.pem" -noout -checkend 0 -checkhost "$domain" >/dev/null 2>&1; then
                printf 'server { listen 80; server_name %s; location ^~ /.well-known/acme-challenge/ { root /var/www/remnamanager-acme; } location / { return 503; } }\n' "$domain" > "$stack/nginx/nginx.conf"
                proxy_compose up -d
                decode "$RM_ACME_B64" > "$manager/acme-setup.sh"
                bash "$manager/acme-setup.sh" "$manager" "$stack" "$(decode "$RM_EMAIL_B64")" "$(decode "$RM_CA_B64")" "$domain"
            fi
            decode "$RM_CONFIG_B64" > "$stack/nginx/nginx.conf"
            proxy_compose run --rm --no-deps nginx nginx -t
            proxy_compose up -d
            proxy_compose exec -T nginx nginx -s reload
        fi
        ;;
    start)
        owned
        if [[ -f $manager/update.pending ]]; then
            [[ -f $manager/subscription.previous ]] || { echo 'missing previous image manifest' >&2; exit 18; }
            cp -p "$manager/subscription.previous" "$stack/subscription/docker-compose.yml"
            compose up -d --no-deps "$subservice"
        else compose start "$subservice"; fi
        healthy
        rm -f "$manager/update.pending"
        proxy_compose start
        ;;
    restart) owned; compose restart "$subservice"; healthy;;
    stop) owned; compose stop "$subservice";;
    status) owned; compose ps --all; proxy_compose ps --all; id=$(compose ps -aq "$subservice"); [[ -z $id ]] || docker inspect --format '{{.State.Status}} {{if .State.Health}}{{.State.Health.Status}}{{end}} restarts={{.RestartCount}} image={{.Image}}' "$id";;
    check)
        owned
        healthy
        panel=$(decode "$RM_PANEL_B64")
        token=$(decode "$RM_TOKEN_B64")
        # Keep tokens out of process arguments and logs, including failed curl.
        printf 'header = "Authorization: Bearer %s"\n' "$token" | curl -fsS --config - --connect-timeout 10 --max-time 20 "https://$panel/api/system/stats" -o /dev/null
        # The public root intentionally rejects requests without a shortUuid.
        openssl s_client -connect "$domain:443" -servername "$domain" -verify_hostname "$domain" -verify_return_error </dev/null >/dev/null 2>&1
        ;;
    update|image)
        owned
        [[ ! -f $manager/update.pending ]] || { echo 'previous image update was interrupted; choose Install/start to recover' >&2; exit 18; }
        source=$(decode "$RM_VALUE_B64")
        [[ -n $source ]] || source=$(cat "$manager/source" 2>/dev/null || printf 'remnawave/subscription-page:latest')
        docker pull "$source"
        digest=$(docker image inspect --format '{{index .RepoDigests 0}}' "$source")
        [[ $digest =~ ^[a-zA-Z0-9./:_-]+@sha256:[a-f0-9]{64}$ ]] || exit 16
        cp -p "$stack/subscription/docker-compose.yml" "$manager/subscription.previous"
        cp -p "$stack/subscription/.env" "$manager/subscription.env.previous"
        printf 'pending\n' > "$manager/update.pending"
        if [[ $layout -ge 2 ]]; then
            sed -E 's#^(    image: ).*$#\1'"$digest"'#' "$manager/subscription.previous" > "$stack/subscription/docker-compose.yml"
        else
            sed -E 's#"image": "[^"]+"#"image": "'"$digest"'"#' "$manager/subscription.previous" > "$stack/subscription/docker-compose.yml"
        fi
        if ! compose config --quiet || ! compose up -d --no-deps "$subservice" || ! healthy; then
            cp -p "$manager/subscription.previous" "$stack/subscription/docker-compose.yml"
            compose up -d --no-deps "$subservice"
            healthy || { echo 'image rollback failed; remote recovery required' >&2; exit 18; }
            rm -f "$manager/update.pending"
            echo 'new image failed; previous image restored' >&2
            exit 17
        fi
        printf '%s\n' "$source" > "$manager/source"
        rm -f "$manager/update.pending"
        ;;
    remove)
        owned
        proxy_compose down
        compose down
        # Only fixed owned files; certificates, ACME and backups are preserved.
        rm -f "$stack/subscription/docker-compose.yml" "$stack/subscription/.env" "$stack/$proxy/docker-compose.yml" "$stack/$proxy/Caddyfile" "$stack/$proxy/nginx.conf" "$stack/.remnamanager-subscription"
        ;;
    *) exit 2;;
esac
