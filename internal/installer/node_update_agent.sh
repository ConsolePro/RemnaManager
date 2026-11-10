set -Eeuo pipefail
umask 077
[[ $RM_JOB_ID =~ ^[a-f0-9]{32}$ && $RM_UPDATE_ID =~ ^[a-f0-9]{32}$ ]]
root=/var/lib/remnamanager-node
stack=/opt/remnanode
dir=$root/updates/$RM_UPDATE_ID
unit=remnamanager-node-update-rollback
[[ $(id -u) == 0 && -d /run/systemd/system ]]
[[ $(cat "$root/job-id") == "$RM_JOB_ID" && $(cat "$stack/.remnamanager-go-node") == "$RM_JOB_ID" && -f $root/ready && ! -f $root/firewall-pending ]]
exec 9>"$root/agent.lock"
flock -w 30 9
exec 3>&1
exec >>"$root/install.log" 2>&1
trap 'echo "Node update failed; see /var/lib/remnamanager-node/install.log" >&3' ERR
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
    [[ $RM_PREVIOUS_IMAGE =~ ^remnawave/node@sha256:[a-f0-9]{64}$ ]]
    [[ ! -e $dir ]]
    [[ ! -L $stack/compose.json && -f $stack/compose.json ]]
    [[ $(jq -r '.services.remnanode.image' "$stack/compose.json") == "$RM_PREVIOUS_IMAGE" ]]
    # Only the image changes; every other Compose value is copied verbatim.
    docker image inspect "$RM_PREVIOUS_IMAGE" >/dev/null
    verify_running "$RM_PREVIOUS_IMAGE"
    docker pull remnawave/node:latest
    image=$(docker image inspect --format '{{index .RepoDigests 0}}' remnawave/node:latest)
    image=${image#docker.io/}
    [[ $image =~ ^remnawave/node@sha256:[a-f0-9]{64}$ ]]
    mkdir -p "$root/updates"; chmod 0700 "$root/updates"
    mkdir -m 0700 "$dir"
    write "$dir/compose.before.json" <"$stack/compose.json"
    printf '%s\n' "$image" | write "$dir/image"
    jq --arg image "$image" '.services.remnanode.image=$image' "$dir/compose.before.json" | write "$dir/compose.after.json"
    docker compose --project-directory "$stack" -f "$dir/compose.after.json" config --quiet
    cat <<'EOF' | write "$dir/rollback.sh"
#!/bin/bash
set -Eeuo pipefail
umask 077
dir=$(dirname "$(readlink -f "$0")")
root=/var/lib/remnamanager-node
stack=/opt/remnanode
exec >>"$root/install.log" 2>&1
exec 9>"$root/agent.lock"
flock -w 180 9
phase=$(cat "$dir/phase")
[[ $phase == trial ]] || exit 0
tmp=$(mktemp "$stack/compose.json.rollback.XXXXXXXX")
cat "$dir/compose.before.json" >"$tmp"; chmod 0600 "$tmp"; mv -f -- "$tmp" "$stack/compose.json"
docker compose --project-directory "$stack" -f "$stack/compose.json" up -d --wait --wait-timeout 120 remnanode
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
    compose up -d --wait --wait-timeout 120 remnanode
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
