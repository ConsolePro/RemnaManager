#!/usr/bin/env bash
# Opt-in integration test for an isolated, empty Remnawave 3.4.8 Docker stack.
# Run a disposable container sharing that test backend's network namespace.
set -Eeuo pipefail
shopt -s inherit_errexit
[[ -f /.dockerenv && ${RM_REAL_PANEL_TEST:-} == 1 && $(id -u) == 0 ]] || { echo 'Explicit disposable test stack required.' >&2; exit 1; }
apt-get update -qq >/tmp/remnamanager-real-apt.log 2>&1
DEBIAN_FRONTEND=noninteractive apt-get install -y -qq curl jq openssl >>/tmp/remnamanager-real-apt.log 2>&1
APP_DIR=/source
export RM_ROOT
RM_ROOT=$(mktemp -d)
trap 'rm -rf -- "$RM_ROOT"' EXIT
umask 077
# shellcheck source=lib/runtime.sh
source "$APP_DIR/lib/runtime.sh"
# shellcheck source=lib/panel.sh
source "$APP_DIR/lib/panel.sh"
printf '{"nodes":{}}' | save "$STATE"
response=$(API_ANONYMOUS=1 api GET /auth/status)
[[ $(jq -r .isRegisterAllowed <<<"$response") == true ]] || die 'Test backend is not empty. Refusing to change an existing installation.'
if curl --noproxy 127.0.0.1 --silent --max-time 5 http://127.0.0.1:3000/api/auth/status -o /dev/null; then die 'Expected production proxy check to close the unheaded request.'; fi
wait_http http://127.0.0.1:3000/api/auth/status -H 'X-Forwarded-Proto: https' -H 'X-Forwarded-For: 127.0.0.1'
printf 'PASS real 3.4.8: reproduce closed socket, then pass readiness with proxy headers\n'
password=Rm9$(random_hex 30)
response=$(API_ANONYMOUS=1 api POST /auth/register "$(jq -n --arg password "$password" '{username:"rmtest",password:$password}')")
token=$(jq -er .accessToken <<<"$response")
response=$(API_TOKEN=$token API_ADMIN=1 api POST /tokens '{"name":"RemnaManager","expiresInDays":3650,"scopes":["*"]}')
manager_token=$(jq -er .token <<<"$response")
state_edit '.token = $token' --arg token "$manager_token"
api GET /nodes >/dev/null
create_subscription_token "$token"
[[ -n $(state_get .subscription_token) ]]
printf 'PASS real 3.4.8: admin registration, manager token and separate subscription token\n'
