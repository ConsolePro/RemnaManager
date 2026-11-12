set -Eeuo pipefail
umask 077
[[ ${RM_JOB_ID:-} =~ ^[a-f0-9]{32}$ ]]
stack=/opt/remnanode
old_root=/var/lib/remnamanager-node
old_web=/var/www/remnamanager-node
root=$stack/state
web=$stack/www
link=/etc/nginx/conf.d/remnamanager-node.conf
conf=$stack/nginx/node.conf
if [[ -f $root/job-id && ! -f $root/layout-pending ]]; then
    [[ $(cat "$root/job-id") == "$RM_JOB_ID" ]]
    printf 'RM_NODE_LAYOUT=1\n'
    exit 0
fi
base=$old_root
if [[ -f $root/layout-pending ]]; then base=$root; fi
[[ $(id -u) == 0 && $(cat "$base/job-id") == "$RM_JOB_ID" && -f $base/ready ]]
[[ -f $stack/.remnamanager-go-node && $(cat "$stack/.remnamanager-go-node") == "$RM_JOB_ID" ]]
[[ ! -f $base/firewall-pending && ! -L $root && ! -L $web && ! -L $old_web && ! -L $old_root ]]
if [[ $base == "$old_root" ]]; then
    [[ ! -e $root && ! -e $web && -d $old_web && -f $link && ! -L $link && ! -e $conf ]]
    grep -q 'Managed by RemnaManager node installer' "$link"
fi
exec 9>"$base/agent.lock"
flock -w 10 9
if [[ -f $base/site-change/phase && $(cat "$base/site-change/phase") == trial ]]; then
    echo 'Finish website recovery before migration.' >&2
    exit 1
fi
if [[ -d $base/updates ]]; then
    while IFS= read -r phase; do
        case $(cat "$phase") in prepared|trial) echo 'Finish node update recovery before migration.' >&2; exit 1;; esac
    done < <(find "$base/updates" -name phase -type f)
fi
rewrite() {
    local from_root=$1 to_root=$2 from_web=$3 to_web=$4
    find "$root" -type f \( -name '*.conf' -o -name '*.sh' -o -name '*.service' -o -name '*.timer' -o -name '*.nft' \) -exec sed -i "s|$from_root|$to_root|g;s|$from_web|$to_web|g" {} +
    sed -i "s|$from_root|$to_root|g;s|$from_web|$to_web|g" "$conf"
    local unit
    for unit in /etc/systemd/system/remnamanager-node*.service /etc/systemd/system/remnamanager-node*.timer; do
        [[ ! -f $unit ]] || sed -i "s|$from_root|$to_root|g;s|$from_web|$to_web|g" "$unit"
    done
}
restore() {
    local status=$?
    trap - ERR
    if [[ -d $root && -f $conf ]]; then rewrite "$root" "$old_root" "$web" "$old_web" || true; fi
    if [[ -L $link ]]; then rm -- "$link"; fi
    if [[ -f $conf ]]; then mv -- "$conf" "$link"; fi
    if [[ -d $web && ! -e $old_web ]]; then mv -- "$web" "$old_web"; fi
    if [[ -d $root && ! -e $old_root ]]; then mv -- "$root" "$old_root"; fi
    systemctl daemon-reload || true
    if nginx -t; then systemctl reload nginx || true; fi
    systemctl start remnamanager-node-acme.timer || true
    exit "$status"
}
trap restore ERR
printf '%s\n' "$RM_JOB_ID" >"$base/layout-pending"
systemctl stop remnamanager-node-acme.timer
mkdir -p "$stack/nginx"
if [[ $base == "$old_root" ]]; then mv -- "$old_root" "$root"; fi
if [[ ! -d $web ]]; then mv -- "$old_web" "$web"; fi
if [[ ! -f $conf ]]; then mv -- "$link" "$conf"; fi
if [[ ! -L $link ]]; then ln -s "$conf" "$link"; fi
rewrite "$old_root" "$root" "$old_web" "$web"
chmod 0755 "$stack" "$stack/nginx" "$web"
nginx -t
systemctl daemon-reload
systemctl reload nginx
systemctl start remnamanager-node-acme.timer
rm -- "$root/layout-pending"
trap - ERR
printf 'RM_NODE_LAYOUT=1\n'
