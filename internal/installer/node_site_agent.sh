set -Eeuo pipefail
umask 077
[[ ${RM_JOB_ID:-} =~ ^[a-f0-9]{32}$ ]]
stack=/opt/remnanode
root=$stack/state
web=$stack/www
if [[ ! -f $root/job-id ]]; then root=/var/lib/remnamanager-node; web=/var/www/remnamanager-node; fi
[[ $(cat "$root/job-id") == "$RM_JOB_ID" && -f $root/ready ]]
[[ ! -f $root/firewall-pending ]]
exec 9>"$root/agent.lock"
flock -w 10 9
dir=$root/site-change
unit=remnamanager-node-site-rollback
[[ ! -L $dir && ! -L $web/site && ! -L $web/site.previous && ! -L $web/site.candidate ]]
mkdir -p "$dir"
report() { printf 'RM_SITE_PHASE=%s\n' "$(cat "$dir/phase" 2>/dev/null || echo missing)"; }
case ${RM_SITE_ACTION:-} in
status) report ;;
apply)
    for pending in "$root"/updates/*/phase "$root"/core-updates/*/phase; do
        [[ -f $pending ]] || continue
        case $(cat "$pending") in prepared|trial) echo 'Finish the pending node/core transaction first.' >&2; exit 1;; esac
    done
    if [[ -f $root/site-change/phase && $(cat "$root/site-change/phase") == trial ]]; then echo 'Finish website recovery first.' >&2; exit 1; fi
    [[ ${RM_SITE_NAME:-} == studio || $RM_SITE_NAME == atlas || $RM_SITE_NAME == journal ]]
    if [[ -d $web/site.previous && $(cat "$dir/phase") == committed ]]; then
        backup=$(mktemp -d "$dir/previous.XXXXXXXX"); rmdir "$backup"; mv -T "$web/site.previous" "$backup"
    fi
    if [[ -d $web/site.candidate && $(cat "$dir/phase" 2>/dev/null || echo missing) != trial ]]; then
        backup=$(mktemp -d "$dir/failed.XXXXXXXX"); rmdir "$backup"; mv -T "$web/site.candidate" "$backup"
    fi
    [[ ! -e $web/site.previous && ! -e $web/site.candidate ]]
    [[ ! -f $dir/phase || $(cat "$dir/phase") == committed || $(cat "$dir/phase") == rolled-back ]]
    install -d -m 0755 "$web/site.candidate"
    (umask 022; printf '%s' "$RM_SITE_B64" | base64 --decode | tar -xzf - -C "$web/site.candidate" --no-same-owner --no-same-permissions)
    [[ -s $web/site.candidate/index.html ]]
    # Capture paths in the rollback file, keeping it independent of the caller.
    { printf '#!/bin/bash\nset -Eeuo pipefail\nweb=%q\ndir=%q\nroot=%q\n' "$web" "$dir" "$root"; cat <<'EOF'
exec 9>"$root/agent.lock"
flock -w 120 9
[[ -f $dir/phase && $(cat "$dir/phase") == trial ]] || exit 0
if [[ -d $web/site.previous ]]; then
    if [[ -d $web/site ]]; then mv -T "$web/site" "$web/site.candidate"; fi
    mv -T "$web/site.previous" "$web/site"
fi
printf 'rolled-back\n' >"$dir/phase"
EOF
    } >"$dir/rollback.sh"
    chmod 0700 "$dir/rollback.sh"
    cat >"/etc/systemd/system/$unit.service" <<EOF
[Unit]
Description=Recover interrupted RemnaManager website change
After=network.target
[Service]
Type=oneshot
ExecStart=/bin/bash $dir/rollback.sh
[Install]
WantedBy=multi-user.target
EOF
    systemctl daemon-reload
    systemctl enable "$unit.service"
    systemctl stop "$unit-trial.timer" || true
    systemd-run --collect --unit="$unit-trial" --on-active=120s --timer-property=AccuracySec=1s /bin/bash "$dir/rollback.sh"
    printf '%s\n' "$RM_SITE_NAME" >"$dir/template"
    printf 'trial\n' >"$dir/phase"
    mv -T "$web/site" "$web/site.previous"
    mv -T "$web/site.candidate" "$web/site"
    report
    ;;
commit)
    [[ $(cat "$dir/phase") == trial ]]
    printf 'committed\n' >"$dir/phase"
    systemctl stop "$unit-trial.timer" || true
    systemctl disable "$unit.service"
    # Retain the previous website as the backup until the next explicit change.
    if [[ -d $web/site.previous ]]; then backup=$(mktemp -d "$dir/previous.XXXXXXXX"); rmdir "$backup"; mv -T "$web/site.previous" "$backup"; fi
    report
    ;;
rollback)
    flock -u 9
    bash "$dir/rollback.sh"
    report
    ;;
*) exit 2 ;;
esac
