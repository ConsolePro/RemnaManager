#!/usr/bin/env bash
ssh_options() {
    local node=$1 identity
    SSH_HOST=$(host "$(jq -er .ssh_host <<<"$node")")
    SSH_PORT=$(port "$(jq -r '.ssh_port // 22' <<<"$node")")
    identity=$(printf '%s:%s' "$SSH_HOST" "$SSH_PORT" | sha256sum); identity=${identity:0:24}
    SSH_DIR=$ROOT/ssh/$identity
    mkdir -p "$SSH_DIR"
    chmod 700 "$SSH_DIR"
    SSH_KEY=$SSH_DIR/id_ed25519 SSH_KNOWN=$SSH_DIR/known_hosts
    SSH_OPTS=(-p "$SSH_PORT" -i "$SSH_KEY" -o "UserKnownHostsFile=$SSH_KNOWN" -o GlobalKnownHostsFile=/dev/null
        -o StrictHostKeyChecking=yes -o IdentitiesOnly=yes -o ConnectTimeout=10 -o ServerAliveInterval=15
        -o ServerAliveCountMax=3 -o ControlMaster=no -o BatchMode=yes)
}
ssh_trust() {
    local candidate fingerprint entered
    candidate=$(mktemp "$SSH_DIR/host-key.XXXXXXXX")
    if ! ssh-keyscan -T 10 -p "$SSH_PORT" -t ed25519 "$SSH_HOST" 2>/dev/null | awk '!/^#/ && NF >= 3 {print; exit}' >"$candidate" || [[ ! -s $candidate ]]; then
        rm -f "$candidate"; die 'Сервер не предоставил SSH Ed25519 host key.'
    fi
    if [[ -f $SSH_KNOWN ]] && cmp -s "$candidate" "$SSH_KNOWN"; then rm -f "$candidate"; return; fi
    fingerprint=$(ssh-keygen -lf "$candidate" -E sha256 | awk '{print $2}')
    printf '\nSSH %s:%s: %s\n' "$SSH_HOST" "$SSH_PORT" "$fingerprint" >&2
    if [[ -f $SSH_KNOWN ]]; then printf 'HOST KEY ИЗМЕНИЛСЯ. Сверьте отпечаток через консоль хостинга; возможна подмена сервера.\n' >&2
    else printf 'Первое подключение. Сверьте отпечаток через консоль хостинга.\n' >&2; fi
    entered=$(ask 'Введите полный SHA256:... отпечаток для доверия')
    if [[ $entered != "$fingerprint" ]]; then rm -f "$candidate"; die 'Отпечаток не подтверждён.'; fi
    mv -f "$candidate" "$SSH_KNOWN"
}
ssh_command() { ssh "${SSH_OPTS[@]}" "root@$SSH_HOST" "$1"; }
ssh_connect() {
    ssh_trust
    if [[ ! -f $SSH_KEY ]]; then ssh-keygen -q -t ed25519 -N '' -C remnamanager -f "$SSH_KEY"; fi
    if ssh_command true >/dev/null 2>&1; then return; fi
    printf 'Ключ не принят. Введите актуальный root-пароль в запросе OpenSSH. Пароль не сохраняется.\n' >&2
    local -a password_opts
    password_opts=("${SSH_OPTS[@]:0:${#SSH_OPTS[@]}-2}" -o BatchMode=no -o PubkeyAuthentication=no)
    ssh "${password_opts[@]}" "root@$SSH_HOST" 'set -eu; umask 077; IFS= read -r key; mkdir -p /root/.ssh; touch /root/.ssh/authorized_keys; chmod 700 /root/.ssh; chmod 600 /root/.ssh/authorized_keys; grep -qxF -- "$key" /root/.ssh/authorized_keys || printf "%s\n" "$key" >> /root/.ssh/authorized_keys' <"$SSH_KEY.pub"
    ssh_command true
}
ssh_deploy() {
    ssh_options "$1"
    ssh_connect
    # The agent consists only of Shell sources; no interpreter installation on the node.
    tar -czf - -C "$APP_DIR" agent.sh lib/runtime.sh lib/render.sh lib/provision.sh lib/warp.sh LICENSES/warp-native-MIT.txt |
        ssh_command 'set -eu; umask 077; mkdir -p /opt/remnamanager-agent; tar xzf - -C /opt/remnamanager-agent'
}
ssh_job() {
    local node=$1 job=$2
    if [[ ${3:-deploy} == deploy ]]; then ssh_deploy "$node"; else ssh_options "$node"; fi
    local remote='RM_DEBUG=0 bash /opt/remnamanager-agent/agent.sh'
    [[ $RM_DEBUG != 1 ]] || remote='RM_DEBUG=1 bash /opt/remnamanager-agent/agent.sh'
    jq --argjson debug "$([[ $RM_DEBUG == 1 ]] && printf true || printf false)" '. + {debug:$debug}' <<<"$job" | ssh_command "$remote"
}
connection() {
    local node=$1 uuid stored address ssh_port
    uuid=$(jq -er .uuid <<<"$node"); valid_uuid "$uuid"
    stored=$(jq -c --arg uuid "$uuid" '.nodes[$uuid] // {}' "$STATE")
    if ! jq -e '.ssh_host' <<<"$stored" >/dev/null; then
        address=$(host "$(ask 'SSH IP/домен' "$(jq -er .address <<<"$node")")")
        ssh_port=$(port "$(ask 'SSH порт' 22)")
        stored=$(jq --arg host "$address" --argjson port "$ssh_port" '. + {ssh_host:$host,ssh_port:$port}' <<<"$stored")
        state_edit '.nodes[$uuid] = $node' --arg uuid "$uuid" --argjson node "$stored"
    fi
    printf '%s\n' "$stored"
}
