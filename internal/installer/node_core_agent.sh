set -Eeuo pipefail
umask 077
[[ ${RM_JOB_ID:-} =~ ^[a-f0-9]{32}$ && ${RM_CORE_ID:-} =~ ^[a-f0-9]{32}$ ]]
stack=/opt/remnanode
root=$stack/state
[[ $(id -u) == 0 && $(cat "$root/job-id") == "$RM_JOB_ID" && -f $root/ready && ! -f $root/firewall-pending ]]
[[ $(cat "$stack/.remnamanager-go-node") == "$RM_JOB_ID" && ! -L $stack/compose.json ]]
exec 9>"$root/agent.lock"
flock -w 30 9
exec 3>&1
exec >>"$root/install.log" 2>&1
dir=$root/core-updates/$RM_CORE_ID
unit=remnamanager-node-core-rollback
compose() { docker compose --project-directory "$stack" -f "$stack/compose.json" "$@"; }
write() { local tmp; [[ ! -L $1 ]]; tmp=$(mktemp "$1.tmp.XXXXXXXX"); cat >"$tmp"; chmod "${2:-0600}" "$tmp"; mv -f -- "$tmp" "$1"; }
report() {
    printf 'RM_CORE_PHASE=%s\n' "$(cat "$dir/phase")" >&3
    printf 'RM_CORE_VERSION=%s\n' "$(cat "$dir/version")" >&3
    printf 'RM_CORE_PATH=%s\n' "$(cat "$dir/path")" >&3
}
verify_core() {
    local container expected actual
    container=$(compose ps -q remnanode)
    [[ -n $container && $(docker inspect --format '{{.State.Running}}' "$container") == true ]]
    actual=$(docker exec "$container" /usr/local/bin/xray version | awk 'NR==1 {print $2}')
    [[ $actual == "${RM_CORE_VERSION#v}" ]]
    expected=$(sha256sum "$(cat "$dir/path")"); expected=${expected%% *}
    # Verify the running process, rather than just the executable on disk.
    docker exec "$container" sh -c 'for exe in /proc/[0-9]*/exe; do target=$(readlink "$exe" 2>/dev/null || true); case "$target" in */xray|*/rw-core) sha256sum "$exe";; esac; done' | grep "^$expected " >/dev/null
}
case ${RM_CORE_ACTION:-} in
status)
    if [[ -f $dir/phase ]]; then report; else echo RM_CORE_PHASE=missing >&3; fi
    ;;
prepare)
    for pending in "$root"/updates/*/phase "$root"/core-updates/*/phase; do
        [[ -f $pending ]] || continue
        case $(cat "$pending") in prepared|trial) echo 'Finish the pending node/core transaction first.' >&2; exit 1;; esac
    done
    if [[ -f $root/site-change/phase && $(cat "$root/site-change/phase") == trial ]]; then echo 'Finish website recovery first.' >&2; exit 1; fi
    [[ ${RM_CORE_VERSION:-} =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ && ! -e $dir ]]
    [[ $(jq -r '.services.remnanode.image' "$stack/compose.json") == "$RM_NODE_IMAGE" ]]
    container=$(compose ps -q remnanode)
    [[ -n $container && $(docker inspect --format '{{.State.Running}}' "$container") == true ]]
    expected_image=$(docker image inspect --format '{{.Id}}' "$RM_NODE_IMAGE")
    [[ -n $expected_image && $(docker inspect --format '{{.Image}}' "$container") == "$expected_image" ]]
    active=$(docker exec "$container" sh -c 'if [ -L /usr/local/bin/rw-core ]; then readlink -f /usr/local/bin/rw-core; else echo /usr/local/bin/xray; fi')
    [[ $active == /usr/local/bin/xray ]] || { echo 'Core is controlled by panel geodata.core; update it in the panel.' >&3; exit 1; }
    # Reject an unrelated override of the same target before editing Compose.
    jq -e '.services.remnanode.volumes // [] | all(.[]; if type=="object" then (.target!="/usr/local/bin/xray" or (.source|startswith("/opt/remnanode/xray/"))) else (contains(":/usr/local/bin/xray")|not) end)' "$stack/compose.json" >/dev/null
    case $(uname -m) in x86_64) arch=amd64;; aarch64) arch=arm64;; *) exit 1;; esac
    asset=$(printf '%s' "$RM_CORE_ASSETS_B64" | base64 --decode | jq -er --arg arch "$arch" '.[$arch]')
    url=$(jq -er '.url' <<<"$asset")
    digest=$(jq -er '.sha256' <<<"$asset")
    [[ $digest =~ ^[a-f0-9]{64}$ && $url == "https://github.com/XTLS/Xray-core/releases/download/$RM_CORE_VERSION/"* ]]
    mkdir -p "$root/core-updates"; mkdir -m 0700 "$dir"
    write "$dir/compose.before.json" <"$stack/compose.json"
    printf '%s\n' "$RM_CORE_VERSION" | write "$dir/version"
    binary=$stack/xray/$RM_CORE_VERSION/$arch/xray
    mkdir -p "$(dirname "$binary")"
    curl -fsSL --proto '=https' --proto-redir '=https' --connect-timeout 15 --max-time 180 --max-filesize 104857600 "$url" -o "$dir/core.zip"
    printf '%s  %s\n' "$digest" "$dir/core.zip" | sha256sum -c -
    command -v unzip >/dev/null || { apt-get update; DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends unzip; }
    unzip -p "$dir/core.zip" xray | write "$dir/xray" 0755
    [[ $(od -An -tx1 -N4 "$dir/xray" | tr -d ' \n') == 7f454c46 ]]
    if [[ -f $binary ]]; then
        cmp -s "$dir/xray" "$binary" || { echo 'Existing core executable differs from the verified release.' >&3; exit 1; }
        rm -- "$dir/xray"
    else
        mv -- "$dir/xray" "$binary"
    fi
    printf '%s\n' "$binary" | write "$dir/path"
    docker cp "$binary" "$container:/tmp/remnamanager-xray-$RM_CORE_ID"
    version=$(docker exec "$container" "/tmp/remnamanager-xray-$RM_CORE_ID" version | awk 'NR==1 {print $2}')
    [[ $version == "${RM_CORE_VERSION#v}" ]]
    docker exec "$container" rm -f "/tmp/remnamanager-xray-$RM_CORE_ID"
    jq --arg source "$binary" '.services.remnanode.volumes=((.services.remnanode.volumes // [] | map(select(if type=="object" then .target!="/usr/local/bin/xray" else true end))) + [{type:"bind",source:$source,target:"/usr/local/bin/xray",read_only:true}])' "$dir/compose.before.json" | write "$dir/compose.after.json"
    docker compose --project-directory "$stack" -f "$dir/compose.after.json" config --quiet
    cat <<'EOF' | write "$dir/rollback.sh" 0700
#!/bin/bash
set -Eeuo pipefail
dir=$(dirname "$(readlink -f "$0")")
root=/opt/remnanode/state
stack=/opt/remnanode
exec 9>"$root/agent.lock"
flock -w 180 9
[[ $(cat "$dir/phase") == trial ]] || exit 0
tmp=$(mktemp "$stack/compose.json.core-rollback.XXXXXXXX")
cat "$dir/compose.before.json" >"$tmp"; chmod 0600 "$tmp"; mv -f -- "$tmp" "$stack/compose.json"
docker compose --project-directory "$stack" -f "$stack/compose.json" up -d --force-recreate --wait --wait-timeout 120 remnanode
echo rolled-back >"$dir/phase.tmp"; mv -f -- "$dir/phase.tmp" "$dir/phase"
EOF
    echo prepared | write "$dir/phase"
    report
    ;;
apply)
    [[ $(cat "$dir/phase") == prepared ]]
    cmp -s "$stack/compose.json" "$dir/compose.before.json"
    cat <<EOF | write "/etc/systemd/system/$unit.service"
[Unit]
Description=Recover interrupted Xray core update
After=docker.service
Requires=docker.service
[Service]
Type=oneshot
ExecStart=/bin/bash $dir/rollback.sh
[Install]
WantedBy=multi-user.target
EOF
    systemctl daemon-reload; systemctl enable "$unit.service"
    systemctl stop "$unit-trial.timer" || true
    systemd-run --collect --unit="$unit-trial" --on-active=300s --timer-property=AccuracySec=1s /bin/bash "$dir/rollback.sh"
    printf '%s\n' "$(( $(date +%s) + 300 ))" | write "$dir/deadline"
    echo trial | write "$dir/phase"
    write "$stack/compose.json" <"$dir/compose.after.json"
    compose up -d --force-recreate --wait --wait-timeout 120 remnanode
    report
    ;;
commit)
    [[ $(cat "$dir/phase") == trial ]]
    (( $(date +%s) < $(cat "$dir/deadline") - 10 ))
    systemctl is-active --quiet "$unit-trial.timer"
    cmp -s "$stack/compose.json" "$dir/compose.after.json"
    verify_core
    echo committed | write "$dir/phase"
    systemctl stop "$unit-trial.timer" || true
    systemctl disable "$unit.service" || true
    report
    ;;
rollback)
    if [[ $(cat "$dir/phase") == prepared ]]; then echo rolled-back | write "$dir/phase"; fi
    flock -u 9
    /bin/bash "$dir/rollback.sh"
    report
    ;;
*) exit 2;;
esac
