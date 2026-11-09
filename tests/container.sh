#!/usr/bin/env bash
# Run only in a disposable container: this installs test tools and test certificates.
set -Eeuo pipefail
if [[ $0 == /source/* ]]; then
    cp -- "$0" /tmp/remnamanager-container-check.sh
    exec bash /tmp/remnamanager-container-check.sh
fi
[[ -f /.dockerenv && ${RM_CONTAINER_TESTS:-} == 1 && $(id -u) == 0 ]] || { echo 'Disposable Docker container required.' >&2; exit 1; }
apt-get update -qq >/tmp/remnamanager-apt.log 2>&1
DEBIAN_FRONTEND=noninteractive apt-get install -y -qq bash jq curl openssl openssh-client tar gzip util-linux nginx shellcheck zip unzip iproute2 >>/tmp/remnamanager-apt.log 2>&1
mkdir -p /work/lib /work/tests /work/dist
cp /source/*.sh /source/README.md /source/VERSION /source/app-files.txt /source/.shellcheckrc /work/
cp /source/lib/*.sh /work/lib/
cp /source/tests/*.sh /work/tests/
cp /source/dist/remnamanager.sh /work/dist/
cd /work
bash manager.sh --check
shellcheck -x install.sh remnamanager.sh manager.sh agent.sh build.sh lib/*.sh tests/*.sh
bash tests/run.sh
bash tests/linux.sh
bash tests/version-integration.sh
