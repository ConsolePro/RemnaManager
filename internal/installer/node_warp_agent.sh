set -Eeuo pipefail
umask 077
[[ ${RM_JOB_ID:-} =~ ^[a-f0-9]{32}$ ]] || exit 2
stack=/opt/remnanode
state=$stack/state
dir=$stack/warp
unit=remnamanager-node-warp
guard=remnamanager-node-warp-killswitch
failure() { printf 'RM_WARP_FAILURE=%s\n' "${stage:-foreign}" >&4; exit 1; }
exec 3>&1 4>&2
stage=foreign
trap failure ERR
[[ $(id -u) == 0 && -d /run/systemd/system ]]
[[ $(cat "$state/job-id") == "$RM_JOB_ID" && -f $state/ready && ! -f $state/firewall-pending ]]
[[ ! -L $dir && ! -L $stack/compose.json ]]
exec 9>"$state/agent.lock"
flock -w 20 9
if [[ -f $dir/owner ]]; then
    [[ $(cat "$dir/owner") == "$RM_JOB_ID" ]]
else
    [[ ! -e $dir || -z $(ls -A "$dir") ]]
    if ip link show rmwarp >/dev/null 2>&1; then failure; fi
    if ip link show warp >/dev/null 2>&1; then failure; fi
    [[ ! -e /etc/systemd/system/$unit.service && ! -e /etc/systemd/system/$guard.service ]]
    if ip -4 rule show | grep -Eq '(^5188[89]:|fwmark 0xcab0)'; then failure; fi
    [[ -z $(ip -4 route show table 51888 2>/dev/null || true) ]]
    if command -v nft >/dev/null && nft list table inet remnamanager_warp_native >/dev/null 2>&1; then failure; fi
    [[ $RM_WARP_ACTION == install ]]
    mkdir -p "$dir"
    chmod 0700 "$dir"
    printf '%s\n' "$RM_JOB_ID" >"$dir/owner"
fi
for path in "$dir/owner" "$dir/wgcf" "$dir/wgcf-account.toml" "$dir/wgcf-profile.conf" "$dir/rmwarp.conf" "$dir/capabilities.json" "$dir/install.log"; do [[ ! -L $path ]]; done
exec >>"$dir/install.log" 2>&1
printf 'WARP action: %s\n' "$RM_WARP_ACTION"
compose() { docker compose --project-directory "$stack" -f "$stack/compose.json" "$@"; }
running_xray() {
    local cid
    cid=$(compose ps -q remnanode)
    [[ -n $cid ]]
    docker exec "$cid" sh -c 'for p in /proc/[0-9]*/comm; do if [ "$(cat "$p" 2>/dev/null)" = xray ]; then d=${p%/comm}; printf "%s:" "${d##*/}"; awk '\''{print $22}'\'' "$d/stat"; exit 0; fi; done; exit 1'
}
case $RM_WARP_ACTION in
checkpoint)
    running_xray >"$dir/xray-before" || printf 'missing\n' >"$dir/xray-before"
    ;;
validate)
    stage=validate
    if [[ -n ${RM_WARP_CONFIG:-} ]]; then
        cid=$(compose ps -q remnanode)
        [[ -n $cid ]]
        # Never print configuration or Xray's validation output: it may contain keys.
        printf '%s' "$RM_WARP_CONFIG" | base64 --decode | docker exec -i "$cid" sh -c '
            umask 077
            config=$(mktemp /tmp/remnamanager-warp.XXXXXXXX.json) || exit 1
            trap '\''rm -f "$config"'\'' EXIT HUP INT TERM
            cat >"$config" || exit 1
            binary=$(command -v xray || true)
            [ -n "$binary" ] || binary=/usr/local/bin/xray
            "$binary" run -test -format json -config "$config" >/dev/null 2>&1
        '
    else
        process=$(running_xray)
        [[ -n $process && -f $dir/xray-before && $process != "$(cat "$dir/xray-before")" ]]
    fi
    ;;
install)
    stage=dependencies
    export DEBIAN_FRONTEND=noninteractive
    apt-get update
    apt-get install -y --no-install-recommends wireguard-tools iproute2 nftables curl jq ca-certificates
    [[ $(jq -r '.services.remnanode.network_mode' "$stack/compose.json") == host ]]
    [[ ! -L /etc/systemd/system/$unit.service && ! -L /etc/systemd/system/$guard.service ]]
    case $(uname -m) in x86_64) arch=amd64;; aarch64|arm64) arch=arm64;; *) exit 1;; esac
    assets=$(printf '%s' "$RM_WARP_ASSETS" | base64 --decode)
    url=$(jq -er --arg arch "$arch" '.[$arch].url' <<<"$assets")
    digest=$(jq -er --arg arch "$arch" '.[$arch].sha256' <<<"$assets")
    [[ $url =~ ^https://github\.com/ViRb3/wgcf/releases/download/v[0-9]+\.[0-9]+\.[0-9]+/wgcf_[0-9]+\.[0-9]+\.[0-9]+_linux_(amd64|arm64)$ && $digest =~ ^[a-f0-9]{64}$ ]]
    stage=download
    if [[ ! -f $dir/wgcf || $(sha256sum "$dir/wgcf" | cut -d' ' -f1) != "$digest" ]]; then
        download=$(mktemp "$dir/wgcf.download.XXXXXXXX")
        curl --proto '=https' --proto-redir '=https' -fLsS --connect-timeout 15 --max-time 120 --max-filesize 33554432 "$url" -o "$download"
        [[ $(stat -c %s "$download") -le 33554432 ]]
        printf '%s  %s\n' "$digest" "$download" | sha256sum --check --status
        chmod 0700 "$download"
        mv -f -- "$download" "$dir/wgcf"
    fi
    stage=register
    cd "$dir"
    if [[ ! -s wgcf-account.toml ]]; then
        for attempt in 1 2 3; do
            printf 'WARP registration attempt %s\n' "$attempt"
            if timeout 90 ./wgcf register --accept-tos && [[ -s wgcf-account.toml ]]; then break; fi
            sleep 2
        done
    fi
    [[ -s wgcf-account.toml ]]
    timeout 60 ./wgcf generate
    [[ -s wgcf-profile.conf ]]
    private=$(awk '/^PrivateKey *=/{sub(/^[^=]*=[[:space:]]*/,"");print;exit}' wgcf-profile.conf)
    public=$(awk '/^PublicKey *=/{sub(/^[^=]*=[[:space:]]*/,"");print;exit}' wgcf-profile.conf)
    address=$(awk -F' *= *' '/^Address *=/{n=split($2,a,",");for(i=1;i<=n;i++){gsub(/ /,"",a[i]);if(a[i]~/^[0-9.]+\/32$/){print a[i];exit}}}' wgcf-profile.conf)
    [[ $private =~ ^[A-Za-z0-9+/]{43}=$ && $public =~ ^[A-Za-z0-9+/]{43}=$ && $address =~ ^[0-9.]+/32$ ]]
    cat >"rmwarp.conf" <<EOF
[Interface]
PrivateKey = $private
Address = $address
MTU = 1280
Table = off
PostUp = ip -4 route replace default dev %i table 51888
PostUp = ip -4 rule add priority 51888 fwmark 51888 lookup 51888
PostUp = ip -4 rule add priority 51889 from $address lookup 51888
PostDown = ip -4 rule del priority 51888 fwmark 51888 lookup 51888 || true
PostDown = ip -4 rule del priority 51889 from $address lookup 51888 || true
PostDown = ip -4 route flush table 51888
[Peer]
PublicKey = $public
AllowedIPs = 0.0.0.0/0
Endpoint = 162.159.192.1:2408
PersistentKeepalive = 25
EOF
    chmod 0600 wgcf-account.toml wgcf-profile.conf rmwarp.conf
    cat >"killswitch.sh" <<'EOF'
#!/bin/bash
set -Eeuo pipefail
exec 8>/opt/remnanode/warp/nft.lock
flock 8
rules=$(mktemp /opt/remnanode/warp/nft.XXXXXXXX)
trap 'rm -f "$rules"' EXIT
if nft list table inet remnamanager_warp_native >/dev/null 2>&1; then printf 'delete table inet remnamanager_warp_native\n' >"$rules"; fi
cat >>"$rules" <<'NFT'
table inet remnamanager_warp_native {
 chain output { type filter hook output priority -150; policy accept;
  meta mark 51888 oifname != "rmwarp" counter drop
 }
}
NFT
nft -f "$rules"
EOF
    chmod 0700 killswitch.sh
    cat >"/etc/systemd/system/$guard.service" <<EOF
[Unit]
Description=RemnaManager native WARP marked-traffic protection
After=nftables.service
Before=$unit.service
[Service]
Type=oneshot
RemainAfterExit=yes
ExecStart=/opt/remnanode/warp/killswitch.sh
ExecReload=/opt/remnanode/warp/killswitch.sh
[Install]
WantedBy=multi-user.target
EOF
    cat >"/etc/systemd/system/$unit.service" <<EOF
[Unit]
Description=RemnaManager native WireGuard WARP
Wants=network-online.target
After=network-online.target $guard.service
Requires=$guard.service
[Service]
Type=oneshot
RemainAfterExit=yes
ExecStart=/usr/bin/wg-quick up /opt/remnanode/warp/rmwarp.conf
ExecStop=/usr/bin/wg-quick down /opt/remnanode/warp/rmwarp.conf
[Install]
WantedBy=multi-user.target
EOF
    stage=interface
    systemctl daemon-reload
    systemctl enable --now "$guard.service"
    systemctl enable "$unit.service"
    systemctl restart "$unit.service"
    stage=trace
    verified=0
    for attempt in 1 2 3 4; do
        printf 'WARP network probe %s\n' "$attempt"
        if curl --proto '=https' -fSs --max-time 12 --interface "${address%/32}" https://www.cloudflare.com/cdn-cgi/trace | grep -Eq '^warp=(on|plus)'; then verified=1; break; fi
        sleep 2
    done
    [[ $verified == 1 ]]
    stage=capabilities
    if [[ ! -f capabilities.json ]]; then
        jq '{added:(["NET_ADMIN","NET_RAW"]-(.services.remnanode.cap_add//[])),absent:(.services.remnanode|has("cap_add")|not)}' "$stack/compose.json" >capabilities.json
    fi
    candidate=$(mktemp "$dir/compose.XXXXXXXX.json")
    jq '.services.remnanode.cap_add=((.services.remnanode.cap_add//[])+["NET_ADMIN","NET_RAW"]|unique)' "$stack/compose.json" >"$candidate"
    if ! jq -e --slurpfile next "$candidate" '. == $next[0]' "$stack/compose.json" >/dev/null; then
        docker compose --project-directory "$stack" -f "$candidate" config --quiet
        cp -p "$stack/compose.json" "$dir/compose.previous.json"
        mv -f -- "$candidate" "$stack/compose.json"
        if ! compose up -d --no-deps remnanode; then
            cp -p "$dir/compose.previous.json" "$stack/compose.json"
            compose up -d --no-deps remnanode || true
            exit 1
        fi
    else rm -f -- "$candidate"; fi
    printf 'installed\n' >phase
    ;;
remove)
    stage=remove
    [[ ! -L /etc/systemd/system/$unit.service && ! -L /etc/systemd/system/$guard.service ]]
    if [[ -f /etc/systemd/system/$unit.service ]]; then systemctl disable --now "$unit.service"; fi
    if [[ -f /etc/systemd/system/$guard.service ]]; then systemctl disable --now "$guard.service"; fi
    if nft list table inet remnamanager_warp_native >/dev/null 2>&1; then nft delete table inet remnamanager_warp_native; fi
    rm -f -- "/etc/systemd/system/$unit.service" "/etc/systemd/system/$guard.service"
    systemctl daemon-reload
    if [[ -f $dir/capabilities.json ]]; then
        candidate=$(mktemp "$dir/compose.XXXXXXXX.json")
        jq --slurpfile owned "$dir/capabilities.json" '.services.remnanode.cap_add=((.services.remnanode.cap_add//[])-$owned[0].added) | if $owned[0].absent and (.services.remnanode.cap_add|length)==0 then del(.services.remnanode.cap_add) else . end' "$stack/compose.json" >"$candidate"
        docker compose --project-directory "$stack" -f "$candidate" config --quiet
        mv -f -- "$candidate" "$stack/compose.json"
        compose config --quiet
        compose up -d --no-deps remnanode
        rm -f -- "$dir/capabilities.json"
    fi
    printf 'removed\n' >"$dir/phase"
    ;;
*) exit 2;;
esac
printf 'RM_WARP_OK=%s\n' "$RM_WARP_ACTION" >&3
