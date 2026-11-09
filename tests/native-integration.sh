#!/usr/bin/env bash
set -Eeuo pipefail
umask 077
[[ -f /.dockerenv && ${RM_CONTAINER_TESTS:-} == 1 ]] || exit 1
cd -- "$(dirname -- "$0")/.."
APP_DIR=$PWD
export RM_ROOT=/opt/remnamanager
source lib/runtime.sh
source lib/warp.sh
mkdir -p "$ROOT/warp-native"
cat <<'EOF' >"$ROOT/warp-native/wgcf-profile.conf"
[Interface]
PrivateKey = AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=
Address = 172.16.0.2/32, fd01::2/128
DNS = 1.1.1.1
[Peer]
PublicKey = AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=
AllowedIPs = 0.0.0.0/0, ::/0
Endpoint = engage.cloudflareclient.com:2408
EOF
warp_profile "$ROOT/warp-native/wgcf-profile.conf" >"$ROOT/warp-native/warp.conf"
wg-quick strip "$ROOT/warp-native/warp.conf" >"$ROOT/warp-native/stripped.conf"
grep -q '^AllowedIPs = 0.0.0.0/0' "$ROOT/warp-native/stripped.conf"
warp_killswitch_files
sh -n "$ROOT/warp-native/killswitch.sh"
grep -q 'meta mark 51888 oifname !=' "$ROOT/warp-native/killswitch.sh"
docker() { :; }
printf '{"services":{"remnanode":{"volumes":["/existing:/existing:ro"],"cap_add":["NET_ADMIN"]}}}' | save /opt/remnanode/compose.override.yaml
warp_container_caps
jq -e '.services.remnanode.cap_add==["NET_ADMIN","NET_RAW"] and .services.remnanode.volumes==["/existing:/existing:ro"]' /opt/remnanode/compose.override.yaml >/dev/null
warp_container_caps true
jq -e '.services.remnanode.cap_add==["NET_ADMIN"] and .services.remnanode.volumes==["/existing:/existing:ro"]' /opt/remnanode/compose.override.yaml >/dev/null
printf 'PASS native WireGuard profile parser, kill-switch and minimal container capabilities\n'
