set -Eeuo pipefail
umask 077
[[ $RM_JOB_ID =~ ^[a-f0-9]{32}$ && $RM_UPDATE_ID =~ ^[a-f0-9]{32}$ && $RM_REINSTALL =~ ^[01]$ ]]
root=/var/lib/remnamanager-node
stack=/opt/remnanode
if [[ -f $stack/state/job-id ]]; then root=$stack/state; fi
dir=$root/updates/$RM_UPDATE_ID
unit=remnamanager-node-update-rollback
[[ $(id -u) == 0 && -d /run/systemd/system ]]
[[ $(cat "$root/job-id") == "$RM_JOB_ID" && $(cat "$stack/.remnamanager-go-node") == "$RM_JOB_ID" && -f $root/ready && ! -f $root/firewall-pending ]]
exec 9>"$root/agent.lock"
flock -w 30 9
exec 3>&1
exec >>"$root/install.log" 2>&1
trap 'echo "Node update failed; see $root/install.log" >&3' ERR
write() {
    local path=$1 tmp
    [[ ! -L $path && ( ! -e $path || -f $path ) ]]
    tmp=$(mktemp "${path}.tmp.XXXXXXXX")
    cat >"$tmp"; chmod 0600 "$tmp"; mv -f -- "$tmp" "$path"
}
compose() { docker compose --project-directory "$stack" -f "$stack/compose.json" "$@"; }
verify_running() {
    local container expected
    container=$(compose ps -q remnanode)
    expected=$(docker image inspect --format '{{.Id}}' "$1")
    [[ -n $container && -n $expected && $(docker inspect --format '{{.State.Running}}' "$container") == true && $(docker inspect --format '{{.Image}}' "$container") == "$expected" ]]
}
report() {
    printf 'RM_UPDATE_PHASE=%s\n' "$(cat "$dir/phase")" >&3
    printf 'RM_UPDATE_IMAGE=%s\n' "$(cat "$dir/image")" >&3
}
case $RM_UPDATE_ACTION in
status)
    if [[ -f $dir/phase ]]; then report; else echo RM_UPDATE_PHASE=missing >&3; fi
    ;;
prepare)
    for pending in "$root"/updates/*/phase "$root"/core-updates/*/phase; do
        [[ -f $pending ]] || continue
        case $(cat "$pending") in prepared|trial) echo 'Finish the pending node/core transaction first.' >&2; exit 1;; esac
    done
    if [[ -f $root/site-change/phase && $(cat "$root/site-change/phase") == trial ]]; then echo 'Finish website recovery first.' >&2; exit 1; fi
    [[ $RM_PREVIOUS_IMAGE =~ ^remnawave/node@sha256:[a-f0-9]{64}$ ]]
    [[ ! -e $dir ]]
    [[ ! -L $stack/compose.json && -f $stack/compose.json ]]
    [[ $(jq -r '.services.remnanode.image' "$stack/compose.json") == "$RM_PREVIOUS_IMAGE" ]]
    # Only the image changes; every other Compose value is copied verbatim.
    if [[ $RM_REINSTALL == 1 ]]; then
        # Repair missing/stopped containers using exactly the recorded version.
        docker pull "$RM_PREVIOUS_IMAGE"
        image=$RM_PREVIOUS_IMAGE
    else
        docker image inspect "$RM_PREVIOUS_IMAGE" >/dev/null
        verify_running "$RM_PREVIOUS_IMAGE"
        docker pull remnawave/node:latest
        image=$(docker image inspect --format '{{index .RepoDigests 0}}' remnawave/node:latest)
        image=${image#docker.io/}
    fi
    [[ $image =~ ^remnawave/node@sha256:[a-f0-9]{64}$ ]]
    mkdir -p "$root/updates"; chmod 0700 "$root/updates"
    mkdir -m 0700 "$dir"
    write "$dir/compose.before.json" <"$stack/compose.json"
    printf '%s\n' "$image" | write "$dir/image"
    printf '%s\n' "$RM_REINSTALL" | write "$dir/reinstall"
    if [[ $RM_REINSTALL == 1 ]]; then
        write "$dir/compose.after.json" <"$dir/compose.before.json"
    else
        jq --arg image "$image" '.services.remnanode.image=$image' "$dir/compose.before.json" | write "$dir/compose.after.json"
    fi
    docker compose --project-directory "$stack" -f "$dir/compose.after.json" config --quiet
    cat <<'EOF' | write "$dir/rollback.sh"
#!/bin/bash
set -Eeuo pipefail
umask 077
dir=$(dirname "$(readlink -f "$0")")
root=/var/lib/remnamanager-node
stack=/opt/remnanode
if [[ -f $stack/state/job-id ]]; then root=$stack/state; fi
exec >>"$root/install.log" 2>&1
exec 9>"$root/agent.lock"
flock -w 180 9
phase=$(cat "$dir/phase")
[[ $phase == trial ]] || exit 0
tmp=$(mktemp "$stack/compose.json.rollback.XXXXXXXX")
cat "$dir/compose.before.json" >"$tmp"; chmod 0600 "$tmp"; mv -f -- "$tmp" "$stack/compose.json"
docker compose --project-directory "$stack" -f "$stack/compose.json" up -d --force-recreate --wait --wait-timeout 120 remnanode
printf 'rolled-back\n' >"$dir/phase.tmp"; mv -f "$dir/phase.tmp" "$dir/phase"
EOF
    echo prepared | write "$dir/phase"
    report
    ;;
apply)
    [[ $(cat "$dir/phase") == prepared ]]
    cmp -s "$stack/compose.json" "$dir/compose.before.json"
    cat <<EOF | write "/etc/systemd/system/$unit.service"
[Unit]
Description=Recover interrupted RemnaManager node update
After=docker.service
Requires=docker.service
[Service]
Type=oneshot
ExecStart=/bin/bash $dir/rollback.sh
[Install]
WantedBy=multi-user.target
EOF
    systemctl daemon-reload
    systemctl enable "$unit.service"
    systemctl reset-failed "$unit.service" || true
    # A different timer unit runs the script directly; the persistent service
    # performs the same recovery after a reboot.
    systemctl stop "$unit-trial.timer" || true
    systemd-run --collect --unit="$unit-trial" --on-active=300s --timer-property=AccuracySec=1s /bin/bash "$dir/rollback.sh"
    printf '%s\n' "$(( $(date +%s) + 300 ))" | write "$dir/deadline"
    echo trial | write "$dir/phase"
    write "$stack/compose.json" <"$dir/compose.after.json"
    if [[ $(cat "$dir/reinstall") == 1 ]]; then
        compose up -d --force-recreate --wait --wait-timeout 120 remnanode
    else
        compose up -d --wait --wait-timeout 120 remnanode
    fi
    verify_running "$(cat "$dir/image")"
    report
    ;;
commit)
    [[ $(cat "$dir/phase") == trial ]]
    (( $(date +%s) < $(cat "$dir/deadline") - 10 ))
    systemctl is-active --quiet "$unit-trial.timer"
    cmp -s "$stack/compose.json" "$dir/compose.after.json"
    verify_running "$(cat "$dir/image")"
    # The rollback script takes the same lock and checks this atomic marker.
    echo committed | write "$dir/phase"
    systemctl stop "$unit-trial.timer" || true
    systemctl disable "$unit.service" || true
    report
    ;;
rollback)
    phase=$(cat "$dir/phase")
    if [[ $phase == prepared ]]; then echo rolled-back | write "$dir/phase"; fi
    # Release the shared lock before invoking the independent recovery script.
    flock -u 9
    /bin/bash "$dir/rollback.sh"
    flock -w 30 9
    systemctl stop "$unit-trial.timer" || true
    report
    ;;
*) exit 2;;
esac
