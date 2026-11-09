#!/usr/bin/env bash
# Integration checks with real Nginx/OpenSSL and mocked service/API operations.
set -Eeuo pipefail
shopt -s inherit_errexit
[[ -f /.dockerenv && ${RM_CONTAINER_TESTS:-} == 1 && $(id -u) == 0 ]] || { echo 'Disposable Docker container required.' >&2; exit 1; }
cd -- "$(dirname -- "$0")/.."
APP_DIR=$PWD
export RM_ROOT=/opt/remnamanager
source lib/runtime.sh
source lib/render.sh
source lib/provision.sh
source lib/panel.sh
umask 077
mkdir -p "$ROOT"
printf '{"nodes":{}}' | save "$STATE"

# Real package configuration: mock only package downloads and service management.
# Test version-specific default TLS rejection, the renewal hook, permissions and
# certificate-based site rendering against nginx's actual configuration parser.
apt-get() { :; }
docker() { :; }
systemctl() { :; }
# shellcheck disable=SC2218
packages panel
for name in panel.example.org sub.example.org node.example.org; do
    mkdir -p "/etc/letsencrypt/live/$name"
    openssl req -x509 -newkey rsa:2048 -nodes -days 1 -subj "/CN=$name" -keyout "/etc/letsencrypt/live/$name/privkey.pem" -out "/etc/letsencrypt/live/$name/fullchain.pem" >/dev/null 2>&1
done
{ http_site panel.example.org; tls_site panel.example.org 443 3000 "$(printf '%064d' 0)"; } | save /etc/nginx/conf.d/rm-panel.conf
{ http_site sub.example.org; tls_site sub.example.org 443 3010; } | save /etc/nginx/conf.d/rm-sub.conf
{ http_site node.example.org; tls_site node.example.org 8443; } | save /etc/nginx/conf.d/rm-node.conf
nginx -t
[[ $(stat -c %a "$STATE") == 600 && $(stat -c %a "$ROOT") == 700 ]]
[[ -x /etc/letsencrypt/renewal-hooks/deploy/remnamanager-nginx ]]
printf 'PASS Linux Nginx/TLS generation and file permissions\n'

# Exercise complete node installation without Docker, DNS or remote services.
packages() { :; }
site() { printf '%s\n' "$*" >"$ROOT/site-call"; }
firewall() { printf '%s\n' "$1" >"$ROOT/firewall-call"; }
docker() { printf '%s\n' "$*" >>"$ROOT/docker-calls"; }
node_install '{"domain":"node.example.org","panel_ip":"192.0.2.1","email":"admin@example.org","secret":"node-secret"}'
jq -e '.services.remnanode.environment.SECRET_KEY == "node-secret"' /opt/remnanode/compose.yaml >/dev/null
jq -e '.domain == "node.example.org"' "$ROOT/node.json" >/dev/null
grep -q '^192.0.2.1$' "$ROOT/firewall-call"
printf 'PASS Linux node installation\n'

# Complete panel bootstrap: retain checkpointed credentials on a second run,
# pin the backend and create separate tokens. No external API calls are made.
download() {
    case $1 in
        */releases/latest) jq -n --arg tag "${MOCK_BACKEND_VERSION:-v1.2.3}" '{tag_name:$tag}' | save "$2";;
        */.env.sample) printf 'POSTGRES_USER=postgres\nPOSTGRES_DB=postgres\nPOSTGRES_PASSWORD=old\nDATABASE_URL=old\n' | save "$2";;
        */docker-compose-prod.yml) printf 'services:\n  remnawave:\n    image: remnawave/backend:latest\n' | save "$2";;
        *) die "Unexpected download $1";;
    esac
}
api() {
    case $1:$2 in
        GET:/auth/status) printf '{"isRegisterAllowed":true}';;
        POST:/auth/register) printf '{"accessToken":"login-test"}';;
        POST:/tokens)
            if [[ $(jq -r .name <<<"$3") == RemnaManager ]]; then printf '{"token":"manager-test"}'
            else printf '{"token":"subscription-test"}'; fi;;
        *) die "Unexpected API call $1 $2";;
    esac
}
wait_http() { :; }
confirm() { return 1; }
printf 'panel.example.org\nsub.example.org\nadmin@example.org\n192.0.2.1\nadmin\n' | panel_install >"$ROOT/panel-install-log"
jq -e '.panel.ready == true and .panel.version == "v1.2.3" and .token == "manager-test" and .subscription_token == "subscription-test"' "$STATE" >/dev/null
grep -q 'image: remnawave/backend:1.2.3' "$PANEL/docker-compose.yml"
password=$(state_get .panel.password)
panel_install </dev/null
[[ $(state_get .panel.password) == "$password" ]]
printf 'PASS Linux panel bootstrap and idempotent credentials\n'

# Verify that a failed backend restart restores Compose and leaves its version
# checkpoint unchanged, with a database/config backup retained.
confirm() { return 0; }
docker() {
    printf '%s\n' "$*" >>"$ROOT/docker-calls"
    if [[ ${MOCK_FAIL_UP:-0} == 1 && " $* " == *' up -d '* && " $* " == *' --wait '* ]]; then return 1; fi
}
export MOCK_BACKEND_VERSION=v2.0.0 MOCK_FAIL_UP=1
if (panel_update) >"$ROOT/update-log" 2>&1; then die 'Failed panel restart was accepted.'; fi
grep -q 'image: remnawave/backend:1.2.3' "$PANEL/docker-compose.yml"
[[ $(state_get .panel.version) == v1.2.3 ]]
[[ -n $(find "$ROOT/backups" -name database.dump -print -quit) ]]
unset MOCK_FAIL_UP MOCK_BACKEND_VERSION
sub_install >"$ROOT/sub-install-log"
grep -q '^REMNAWAVE_API_TOKEN=subscription-test$' "$SUB/.env"
jq -e '.subscription == true' "$STATE" >/dev/null
printf 'PASS Linux panel rollback, backup and subscription token isolation\n'

# Verify SHA256 and rollback of an Xray binary override using a tiny executable
# fixture, preserving unrelated mounts. No upstream release is downloaded.
mkdir -p "$ROOT/xray-fixture"
printf '#!/bin/sh\necho fixture-xray\n' >"$ROOT/xray-fixture/xray"
(cd "$ROOT/xray-fixture"; zip -q fixture.zip xray)
sha256sum "$ROOT/xray-fixture/fixture.zip" >"$ROOT/xray-fixture/fixture.dgst"
download() {
    case $1 in
        */releases/latest) printf '{"tag_name":"vtest","assets":[{"name":"Xray-linux-64.zip","browser_download_url":"https://fixture.example/archive"},{"name":"Xray-linux-64.zip.dgst","browser_download_url":"https://fixture.example/digest"}]}' | save "$2";;
        */archive) save "$2" <"$ROOT/xray-fixture/fixture.zip";;
        */digest) save "$2" <"$ROOT/xray-fixture/fixture.dgst";;
        *) die "Unexpected fixture download $1";;
    esac
}
printf '{"services":{"remnanode":{"volumes":["/foo:/bar:ro"]}}}' | save /opt/remnanode/compose.override.yaml
cp /opt/remnanode/compose.override.yaml "$ROOT/override-original"
export MOCK_FAIL_UP=1
if (xray_update) >"$ROOT/xray-update-log" 2>&1; then die 'Failed Xray restart was accepted.'; fi
cmp /opt/remnanode/compose.override.yaml "$ROOT/override-original"
unset MOCK_FAIL_UP
printf 'PASS Linux Xray checksum and override rollback\n'

# Restore checks for the real installer entrypoint below.
unset -f api wait_http confirm

# A new self-contained installer runs all the way to an interactive exit, then
# updates in place, retaining the state and migrating an existing Telegram unit.
printf '{"token":"123:ABC","chat":"-42"}' | save "$ROOT/telegram.json"
printf '[Service]\nExecStart=/usr/bin/python3 /opt/remnamanager/app/remnamanager.py --notify\n' > /etc/systemd/system/remnamanager-monitor.service
mkdir -p "$ROOT/mock-bin"
cat >"$ROOT/mock-bin/systemctl" <<'EOF'
#!/bin/sh
exit 0
EOF
chmod 700 "$ROOT/mock-bin/systemctl"
export PATH="$ROOT/mock-bin:$PATH"
bash build.sh
printf '0\n' | sh dist/remnamanager.sh
grep -q 'ExecStart=/bin/bash /opt/remnamanager/app/manager.sh --notify' /etc/systemd/system/remnamanager-monitor.service
printf '0\n' | sh dist/remnamanager.sh
jq -e '.nodes == {} and .panel.ready == true and .token == "manager-test"' "$STATE" >/dev/null
[[ ! -f $ROOT/app/remnamanager.py ]]
printf 'PASS Linux standalone install, repeat install and Telegram migration\n'
remna_manager --check
[[ $(grep -Fc '. /etc/profile.d/remnamanager.sh # RemnaManager shortcuts' /root/.bashrc) == 1 ]]
bash --noprofile --norc -ic '
    . /etc/profile.d/remnamanager.sh
    remna_manager() { printf "shortcut launched\n" > /opt/remnamanager/shortcut-result; }
    rm
    test "$(cat /opt/remnamanager/shortcut-result)" = "shortcut launched" || exit 1
    touch /opt/remnamanager/shortcut-delete-test
    rm -f -- /opt/remnamanager/shortcut-delete-test
    test ! -e /opt/remnamanager/shortcut-delete-test || exit 1
'
printf 'PASS installed remna_manager and rm shortcut with ordinary deletion preserved\n'
