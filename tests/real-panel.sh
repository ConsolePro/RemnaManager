#!/usr/bin/env bash
# Opt-in integration test for an isolated, empty Remnawave 3.4.8 Docker stack.
# Run a disposable container sharing that test backend's network namespace.
set -Eeuo pipefail
shopt -s inherit_errexit
trap 'printf "Real integration failed near line %s\n" "$LINENO" >&2' ERR
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
source "$APP_DIR/lib/render.sh"
source "$APP_DIR/lib/ssh.sh"
source "$APP_DIR/lib/nodes.sh"
source "$APP_DIR/lib/topology.sh"
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
TEST_HOSTS='[]'
TEST_NODES='[]'
TEST_SQUADS='[]'
for number in 1 2; do
    private=$(openssl genpkey -algorithm X25519 -outform DER | tail -c 32 | openssl base64 -A | tr '+/' '-_' | tr -d '=')
    response=$(api POST /config-profiles "$(jq -n --arg name "RM_Test_$number" --argjson config "$(reality_config "cover-$number.example.org" "$private" "$(random_hex 8)" "RM_TEST_REALITY_$number")" '{name:$name,config:$config}')")
    profile=$(jq -er .uuid <<<"$response")
    inbound=$(api GET "/config-profiles/$profile/inbounds" | jq -er --arg tag "RM_TEST_REALITY_$number" '.inbounds[]|select(.tag==$tag)|.uuid')
    response=$(api POST /nodes "$(jq -n --arg name "Test_$number" --arg address "192.0.2.$number" --arg profile "$profile" --arg inbound "$inbound" '{name:$name,address:$address,port:2222,configProfile:{activeConfigProfileUuid:$profile,activeInbounds:[$inbound]}}')")
    node=$(jq -er .uuid <<<"$response")
    TEST_NODES=$(jq --arg node "$node" '.+[$node]' <<<"$TEST_NODES")
    squad=$(api POST /internal-squads "$(jq -n --arg name "RM_Test_$number" --arg inbound "$inbound" '{name:$name,inbounds:[$inbound]}')" | jq -er .uuid)
    TEST_SQUADS=$(jq --arg squad "$squad" '.+[$squad]' <<<"$TEST_SQUADS")
    response=$(api POST /hosts "$(jq -n --arg name "Germany_$number" --arg address "192.0.2.$number" --arg node "$node" --arg profile "$profile" --arg inbound "$inbound" --arg sni "cover-$number.example.org" '{remark:$name,address:$address,port:443,sni:$sni,fingerprint:"chrome",nodes:[$node],inbound:{configProfileUuid:$profile,configProfileInboundUuid:$inbound}}')")
    host_uuid=$(jq -er .uuid <<<"$response")
    TEST_HOSTS=$(jq --arg uuid "$host_uuid" '.+[$uuid]' <<<"$TEST_HOSTS")
    state_edit '.nodes[$node]={profile:$profile,inbound:$inbound,squad:$squad,host_uuid:$host,ssh_host:$address,ssh_port:22}' --arg node "$node" --arg profile "$profile" --arg inbound "$inbound" --arg squad "$squad" --arg host "$host_uuid" --arg address "192.0.2.$number"
done
response=$(api POST /users "$(jq -n --arg squad "$(jq -er '.[0]' <<<"$TEST_SQUADS")" '{username:"rm_subscriber",expireAt:"2099-12-31T00:00:00.000Z",trafficLimitBytes:0,activeInternalSquads:[$squad]}')")
subscriber=$(jq -er '.id|tostring' <<<"$response")
short=$(jq -er .shortUuid <<<"$response")
pick_many() { printf '%s\n' "$TEST_HOSTS"; }
ask() { printf 'Germany\n'; }
confirm() { return 0; }
balancer_create
curl --fail --silent --show-error -H 'X-Forwarded-Proto: https' -H 'X-Forwarded-For: 127.0.0.1' "http://127.0.0.1:3000/api/sub/$short/v2ray-json" >"$ROOT/subscription.json"
jq -e '[.[]|select(.remarks=="Germany")]|length==1' "$ROOT/subscription.json" >/dev/null
jq -e '.[]|select(.remarks=="Germany")|[.outbounds[]|select(.tag|startswith("proxy"))]|length==2' "$ROOT/subscription.json" >/dev/null
jq -e '.[]|select(.remarks=="Germany")|[.outbounds[]|select(.tag|startswith("proxy"))|.streamSettings.realitySettings.publicKey]|unique|length==2' "$ROOT/subscription.json" >/dev/null
file=$(find "$ROOT/topology" -name 'balancer-*.json' -print -quit)
balancer_remove_file "$file"
state_edit 'del(.balancers)'
printf 'PASS real 3.4.8: one JSON entry, two distinct Reality outbounds and balancer cleanup\n'
select_node() {
    index=0
    [[ ! -f $ROOT/selection ]] || index=$(cat "$ROOT/selection")
    printf '%s' "$((index+1))" >"$ROOT/selection"
    api GET "/nodes/$(jq -er --argjson index "$index" '.[$index]' <<<"$TEST_NODES")"
}
ssh_job() { jq '.config' <<<"$2" >"$ROOT/validated-config.json"; }
bridge_create
file=$(find "$ROOT/topology" -name 'bridge-*.json' -print -quit)
jq -e '.routing.rules|any(.outboundTag=="RM_BRIDGE")' "$ROOT/validated-config.json" >/dev/null
bridge_remove_file "$file"
api GET "/users/$subscriber" | jq -e --arg id "$subscriber" '(.id|tostring)==$id' >/dev/null
printf 'PASS real 3.4.8: bridge user, Reality outbound, profile patch and independent cleanup\n'
