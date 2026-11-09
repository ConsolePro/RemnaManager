#!/usr/bin/env bash
set -Eeuo pipefail
shopt -s inherit_errexit
umask 077
APP_DIR=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)
cd "$APP_DIR"
if [[ -z ${RM_TEST_ROOT:-} ]]; then
    RM_TEST_ROOT=$(mktemp -d)
    trap 'rm -rf -- "$RM_TEST_ROOT"' EXIT
fi
export RM_TEST_ROOT
ROOT=$RM_TEST_ROOT
export RM_ROOT=$ROOT
source lib/runtime.sh
source lib/render.sh
source lib/provision.sh
source lib/ssh.sh
source lib/panel.sh
source lib/nodes.sh
source lib/monitor.sh
source lib/menu.sh
fail() { printf 'ASSERTION FAILED: %s\n' "$*" >&2; exit 1; }
equal() { [[ $1 == "$2" ]] || fail "expected [$2], got [$1]"; }
contains() { [[ $1 == *"$2"* ]] || fail "missing [$2]"; }
reject() {
    if bash -c 'set -euo pipefail; source lib/runtime.sh; "$@"' test "$@" >"$ROOT/rejected" 2>&1; then fail "accepted $*"; fi
}
test_validation() {
    local value
    for value in 'example.org;reboot' '-oProxyCommand=id' 'https://example.org' '*.example.org' 'a..org' 'a.-bad.org' '127.0.0.1' 'example.org.' $'a.org\n}'; do reject domain "$value"; done
    equal "$(domain '  Node.Example.org ')" node.example.org
    for value in '-oProxyCommand=id' 'root@host' 'host;id' $'a\nwhoami' 'a b' /tmp/a; do reject host "$value"; done
    equal "$(host '2001:db8::1')" '2001:db8::1'
    equal "$(port 00022)" 22
    equal "$(port 65535)" 65535
    for value in 0 -1 65536 '22;id' 9999999999999999999999; do reject port "$value"; done
    equal "$(valid_ip 192.0.2.1)" 192.0.2.1
    reject valid_ip '999.0.0.1'
    reject valid_ip '1.2.3.4;id'
}
test_storage() {
    local secret='$(touch /tmp/should-not-exist) "quoted"'
    jq -n --arg secret "$secret" '{nodes:{},secret:$secret}' | save "$STATE"
    equal "$(state_get '.secret')" "$secret"
    state_edit '.nodes.example = {port:$port}' --argjson port 22
    equal "$(state_get '.nodes.example.port')" 22
    printf '# comment\nFOO=old\nBAR=keep\n' | save "$ROOT/.env"
    env_set "$ROOT/.env" FOO new
    env_set "$ROOT/.env" SECRET "$secret"
    env_set "$ROOT/.env" FOO new
    equal "$(grep -c '^FOO=' "$ROOT/.env")" 1
    contains "$(cat "$ROOT/.env")" 'BAR=keep'
    contains "$(cat "$ROOT/.env")" "$secret"
    printf 'unchanged\n' | save "$ROOT/atomic"
    equal "$(find "$ROOT" -name '*.tmp.*' | wc -l | tr -d '[:space:]')" 0
    # Windows does not implement POSIX mode bits; verify permissions on Linux.
    if [[ $(uname -s) == Linux ]]; then equal "$(stat -c %a "$STATE")" 600; fi
}
test_configs() {
    local config secret='abc"$(id)'
    config=$(tls_site panel.example.org 443 3000 "$(printf '%064d' 0)")
    contains "$config" 'Secure; HttpOnly; SameSite=Strict'
    contains "$config" 'location @panel'
    contains "$config" 'try_files $uri $uri/ =404'
    contains "$config" 'access_log off'
    contains "$config" 'proxy_set_header X-Forwarded-For $remote_addr'
    config=$(tls_site node.example.org 8443)
    contains "$config" 'listen 127.0.0.1:8443 ssl http2'
    [[ $config != *'listen 443'* && $config != *'listen [::]'* ]] || fail 'public node listener'
    contains "$(http_site node.example.org)" 'location ^~ /.well-known/acme-challenge/'
    config=$(reality_config node.example.org key 0123456789abcdef)
    jq -e '.inbounds[0] | .port == 443 and .streamSettings.network == "raw" and .streamSettings.realitySettings.target == "127.0.0.1:8443" and .settings.clients == []' <<<"$config" >/dev/null
    equal "$(node_compose "$secret" | jq -r .services.remnanode.environment.SECRET_KEY)" "$secret"
    subscription_compose | jq -e '.services.subscription.ports == ["127.0.0.1:3010:3010"]' >/dev/null
    checker_compose | jq -e '.services.checker.ports == ["127.0.0.1:2112:2112"]' >/dev/null
    local theme
    for theme in studio journal workshop; do contains "$(site_html "$theme")" '<!doctype html>'; done
}
test_warp() {
    local original enabled again restored
    original=$(reality_config node.example.org key short | jq '.outbounds += [{tag:"CUSTOM",protocol:"freedom"}]')
    enabled=$(warp_config true '["example.com"]' <<<"$original")
    again=$(warp_config true '["example.com"]' <<<"$enabled")
    equal "$again" "$enabled"
    equal "$(jq -r '.routing.rules[0].domain[0]' <<<"$enabled")" domain:example.com
    restored=$(warp_config false <<<"$enabled")
    equal "$(jq -S . <<<"$restored")" "$(jq -S . <<<"$original")"
}
test_ssh_pinning() {
    local node fingerprint changed
    ssh-keygen -q -t ed25519 -N '' -f "$ROOT/host1"
    ssh-keygen -q -t ed25519 -N '' -f "$ROOT/host2"
    ssh-keyscan() { printf 'node.example.org %s\n' "$(cut -d ' ' -f 1,2 "$ROOT/host${KEY_NUMBER:-1}.pub")"; }
    node='{"ssh_host":"node.example.org","ssh_port":2222}'
    ssh_options "$node"
    contains "${SSH_OPTS[*]}" StrictHostKeyChecking=yes
    contains "${SSH_OPTS[*]}" GlobalKnownHostsFile=/dev/null
    fingerprint=$(ssh-keygen -lf "$ROOT/host1.pub" -E sha256 | awk '{print $2}')
    ssh_trust <<<"$fingerprint"
    ssh_trust </dev/null
    cp "$SSH_KNOWN" "$ROOT/pinned"
    # Rejected trust runs in a child, so die cannot exit the test process.
    export KEY_NUMBER=2
    export -f ssh-keyscan ssh_trust ask die
    export ROOT SSH_DIR SSH_PORT SSH_HOST SSH_KNOWN
    if bash -c 'set -euo pipefail; ssh_trust' <<<wrong >"$ROOT/trust-log" 2>&1; then fail 'changed key accepted'; fi
    cmp "$ROOT/pinned" "$SSH_KNOWN"
}
test_nginx_rollback() {
    printf 'old\n' | save "$ROOT/nginx.conf"
    # shellcheck disable=SC2329,SC2317
    nginx_reload() { [[ $(cat "$ROOT/nginx.conf") == old ]]; }
    export -f nginx_reload nginx_write save die
    export ROOT
    if printf 'new\n' | bash -c 'set -euo pipefail; nginx_write "$ROOT/nginx.conf"' >"$ROOT/nginx-log" 2>&1; then fail 'bad nginx config accepted'; fi
    equal "$(cat "$ROOT/nginx.conf")" old
}
mock_api_curl() {
    local output='' arg
    while (($#)); do
        arg=$1; shift
        if [[ $arg == -o ]]; then output=$1; shift
        elif [[ $arg == -H && $1 == @* ]]; then cp "${1#@}" "$ROOT/request-headers"; shift; fi
    done
    if [[ ${MOCK_API_CODE:-200} == 200 ]]; then printf '{"response":{"ok":true}}' >"$output"
    else printf '{"error":"DO_NOT_ECHO_SECRET"}' >"$output"; fi
    printf '%s' "${MOCK_API_CODE:-200}"
}
test_api_errors() {
    printf '{"nodes":{},"token":"test-token"}' | save "$STATE"
    curl() { mock_api_curl "$@"; }
    equal "$(api GET /nodes | jq -r .ok)" true
    [[ $(cat "$ROOT/request-headers") != *'X-Remnawave-Client-Type'* ]] || fail 'ordinary API token marked as browser'
    API_ADMIN=1 api POST /tokens '{}' >/dev/null
    contains "$(cat "$ROOT/request-headers")" 'X-Remnawave-Client-Type: browser'
    export -f api state_get curl mock_api_curl die
    export ROOT STATE MOCK_API_CODE=400
    if bash -c 'set -euo pipefail; api POST /tokens "{}"' >"$ROOT/api-log" 2>&1; then fail 'API error accepted'; fi
    contains "$(cat "$ROOT/api-log")" 'HTTP 400'
    [[ $(cat "$ROOT/api-log") != *DO_NOT_ECHO_SECRET* ]] || fail 'secret echoed'
    equal "$(find "$ROOT" -name 'api-response.*' -o -name 'api-headers.*' | wc -l | tr -d '[:space:]')" 0
}
test_readiness() {
    sleep() { :; }
    curl() { local count=0; [[ ! -f $ROOT/count ]] || count=$(cat "$ROOT/count"); ((count+=1)); printf '%s' "$count" >"$ROOT/count"; if ((count < 2)); then printf 503; else printf 401; fi; }
    wait_http http://localhost
    equal "$(cat "$ROOT/count")" 2
    # Production Remnawave destroys the socket unless both headers are present.
    curl() {
        if [[ " $* " == *' X-Forwarded-Proto: https '* && " $* " == *' X-Forwarded-For: 127.0.0.1 '* ]]; then printf 200
        else printf 000; return 52; fi
    }
    wait_http http://127.0.0.1:3000/api/auth/status -H 'X-Forwarded-Proto: https' -H 'X-Forwarded-For: 127.0.0.1'
    # shellcheck disable=SC2329,SC2317
    curl() { printf 503; }
    export -f curl sleep wait_http die
    if RM_HTTP_ATTEMPTS=2 bash -c 'set -euo pipefail; wait_http http://localhost' >"$ROOT/http-log" 2>&1; then fail 'unready HTTP accepted'; fi
}
resume_worker() {
    ssh_deploy() { :; }
    ssh_job() { :; }
    sleep() { :; }
    api() {
        printf '%s %s\n' "$1" "$2" >>"$ROOT/api-calls"
        case $1:$2 in
            GET:/keygen) printf '{"secretKey":"node-secret"}';;
            POST:/config-profiles) printf '{"uuid":"00000000-0000-0000-0000-000000000001"}';;
            GET:/config-profiles/*/inbounds) printf '{"inbounds":[{"tag":"VLESS_REALITY","uuid":"00000000-0000-0000-0000-000000000002"}]}';;
            POST:/nodes) printf '{"uuid":"00000000-0000-0000-0000-000000000003"}';;
            POST:/hosts) if [[ ! -f $ROOT/retry ]]; then die 'simulated API outage'; fi; printf '{"uuid":"00000000-0000-0000-0000-000000000004"}';;
            POST:/internal-squads) printf '{"uuid":"00000000-0000-0000-0000-000000000005"}';;
            GET:/nodes/*) printf '{"isConnected":true}';;
            *) die "Unexpected API $*";;
        esac
    }
    finish_node "$ROOT/pending/test.json"
}
test_resume() {
    printf '{"nodes":{}}' | save "$STATE"
    printf '{"name":"node","address":"node.example.org","ssh_host":"node.example.org","ssh_port":22,"domain":"cover.example.org","email":"a@example.org","panel_ip":"192.0.2.1"}' | save "$ROOT/pending/test.json"
    if bash tests/cases.sh resume_worker >"$ROOT/first-log" 2>&1; then fail 'simulated outage ignored'; fi
    equal "$(jq -r .uuid "$ROOT/pending/test.json")" 00000000-0000-0000-0000-000000000003
    touch "$ROOT/retry"
    bash tests/cases.sh resume_worker >"$ROOT/retry-log"
    equal "$(grep -c '^POST /nodes$' "$ROOT/api-calls")" 1
    equal "$(grep -c '^POST /config-profiles$' "$ROOT/api-calls")" 1
    [[ ! -f $ROOT/pending/test.json ]] || fail 'completed journal retained'
    jq -e '.nodes["00000000-0000-0000-0000-000000000003"] | .profile != null and (has("secret")|not)' "$STATE" >/dev/null
}
notify_worker() {
    nodes() { cat "$ROOT/nodes.json"; }
    curl() { cat >"$ROOT/message.json"; if [[ -f $ROOT/send-fails ]]; then return 7; fi; printf '{"ok":true}'; }
    notify
}
test_notifications() {
    printf '{"token":"123:ABC","chat":"-42"}' | save "$ROOT/telegram.json"
    printf '[{"uuid":"n1","name":"node","isConnected":true,"isDisabled":false}]' | save "$ROOT/nodes.json"
    bash tests/cases.sh notify_worker
    [[ ! -f $ROOT/message.json ]] || fail 'initial notification sent'
    printf '[{"uuid":"n1","name":"node","isConnected":true,"isDisabled":true}]' | save "$ROOT/nodes.json"
    touch "$ROOT/send-fails"
    if bash tests/cases.sh notify_worker >"$ROOT/notify-log" 2>&1; then fail 'delivery failure ignored'; fi
    equal "$(jq -r .n1 "$ROOT/monitor-state.json")" true
    rm "$ROOT/send-fails"
    bash tests/cases.sh notify_worker
    equal "$(jq -r .text "$ROOT/message.json")" 'node: OFFLINE'
    equal "$(jq -r .n1 "$ROOT/monitor-state.json")" false
}
test_fail_fast() {
    if bash -c 'set -Eeuo pipefail; shopt -s inherit_errexit; f() { false; echo BAD; }; value=$(f); echo "$value"' >"$ROOT/fast-log" 2>&1; then fail 'errexit disabled'; fi
    [[ ! -s $ROOT/fast-log ]] || fail 'failed operation continued'
}
test_distribution() {
    local archive=$ROOT/app.tar file digest
    sh -n dist/remnamanager.sh
    sed '1,/^__REMNAMANAGER_PAYLOAD__$/d' dist/remnamanager.sh | base64 -d >"$archive"
    digest=$(sha256sum "$archive"); digest=${digest%% *}
    grep -Fq "'$digest'" dist/remnamanager.sh || fail 'bundle SHA256 mismatch'
    mkdir -p "$ROOT/extracted"
    tar -xf "$archive" -C "$ROOT/extracted"
    equal "$(find "$ROOT/extracted" -name '*.py' | wc -l | tr -d '[:space:]')" 0
    for file in manager.sh agent.sh lib/runtime.sh lib/render.sh lib/provision.sh lib/ssh.sh lib/panel.sh lib/nodes.sh lib/monitor.sh lib/menu.sh lib/install.sh lib/shortcuts.sh README.md; do cmp "$file" "$ROOT/extracted/$file"; done
    bash -n "$ROOT/extracted/manager.sh"
}
test_bundle() {
    local first second
    bash build.sh >"$ROOT/build-log"
    first=$(sha256sum dist/remnamanager.sh)
    bash build.sh >>"$ROOT/build-log"
    second=$(sha256sum dist/remnamanager.sh)
    equal "$first" "$second"
    test_distribution
}
case ${1:-} in
    resume_worker|notify_worker) "$1";;
    *) "test_${1:?test name required}";;
esac
