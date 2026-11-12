#!/bin/bash
# Only run inside a disposable Docker network namespace with NET_ADMIN.
set -Eeuo pipefail
[[ ${RM_CONTAINER_TESTS:-0} == 1 && -f /.dockerenv ]] || exit 2
agent=${1:?path to WARP agent}
marked=$(sed -nE 's/^PostUp = ip -4 rule add priority ([0-9]+) fwmark.*/\1/p' "$agent")
source_priority=$(sed -nE 's/^PostUp = ip -4 rule add priority ([0-9]+) from.*/\1/p' "$agent")
[[ $marked =~ ^[0-9]+$ && $source_priority =~ ^[0-9]+$ ]]
((marked>0 && source_priority>0 && marked<32766 && source_priority<32766 && marked!=source_priority))
device() { awk '{for(i=1;i<NF;i++)if($i=="dev"){print $(i+1);exit}}'; }
original=$(ip -4 route get 1.1.1.1 | device)
[[ -n $original && $original != rmwarp ]]
cleanup() {
    ip -4 rule del priority "$marked" fwmark 51888 lookup 51888 2>/dev/null || true
    ip -4 rule del priority "$source_priority" from 172.16.0.2/32 lookup 51888 2>/dev/null || true
    ip -4 rule del priority 51888 fwmark 51888 lookup 51888 2>/dev/null || true
    ip -4 rule del priority 51889 from 172.16.0.2/32 lookup 51888 2>/dev/null || true
    ip -4 route flush table 51888
    ip link del rmwarp
}
ip link add rmwarp type dummy
trap cleanup EXIT
ip address add 172.16.0.2/32 dev rmwarp
ip link set rmwarp up
ip -4 route add default dev rmwarp table 51888
ip -4 rule add priority 51888 fwmark 51888 lookup 51888
ip -4 rule add priority 51889 from 172.16.0.2/32 lookup 51888
[[ $(ip -4 route get 1.1.1.1 from 172.16.0.2 | device) == "$original" ]]
[[ $(ip -4 route get 1.1.1.1 mark 51888 | device) == "$original" ]]
printf 'Old priorities reproduce the bug: source and mark use %s\n' "$original"
ip -4 rule add priority "$marked" fwmark 51888 lookup 51888
ip -4 rule add priority "$source_priority" from 172.16.0.2/32 lookup 51888
[[ $(ip -4 route get 1.1.1.1 from 172.16.0.2 | device) == rmwarp ]]
[[ $(ip -4 route get 1.1.1.1 mark 51888 | device) == rmwarp ]]
[[ $(ip -4 route get 1.1.1.1 | device) == "$original" ]]
printf 'Agent priorities %s/%s use rmwarp; ordinary traffic retains %s\n' "$marked" "$source_priority" "$original"
