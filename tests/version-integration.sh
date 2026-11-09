#!/usr/bin/env bash
set -Eeuo pipefail
shopt -s inherit_errexit
[[ -f /.dockerenv && ${RM_CONTAINER_TESTS:-} == 1 && $(id -u) == 0 ]] || { echo 'Disposable Docker container required.' >&2; exit 1; }
cd -- "$(dirname -- "$0")/.."
APP_DIR=$PWD
export RM_ROOT=/opt/remnamanager
source lib/runtime.sh
source lib/versions.sh
original_state=$(sha256sum "$STATE")
original_version=$(cat "$ROOT/app/VERSION")
fixture=$(mktemp -d)
trap 'rm -rf -- "$fixture"' EXIT
mkdir -p "$fixture/lib"
while IFS= read -r file; do cp --parents "$file" "$fixture/"; done <app-files.txt
cp build.sh remnamanager.sh "$fixture/"
printf '1.0.1\n' >"$fixture/VERSION"
bash "$fixture/build.sh"
confirm() { return 0; }
download() { save "$2" <"$fixture/dist/remnamanager.sh"; }
versions_prepare '{"version":"1.0.1","sha":"1111111111111111111111111111111111111111"}'
versions_activate
[[ $(cat "$ROOT/app/VERSION") == 1.0.1 ]]
[[ $(sha256sum "$STATE") == "$original_state" ]]
snapshot=''
for metadata in "$ROOT"/versions/*/metadata.json; do
    if [[ $(jq -r .version "$metadata") == "$original_version" ]]; then snapshot=$(basename "$(dirname "$metadata")"); fi
done
[[ -n $snapshot ]]
versions_prepare_rollback "$snapshot"
versions_activate
[[ $(cat "$ROOT/app/VERSION") == "$original_version" ]]
[[ $(sha256sum "$STATE") == "$original_state" ]]
printf 'PASS version update, code snapshot and rollback with panel state preserved\n'
# A corrupt payload must be rejected before the installed app is changed.
printf 'broken\n' >"$fixture/dist/remnamanager.sh"
if (versions_prepare '{"version":"1.0.2","sha":"2222222222222222222222222222222222222222"}') >"$fixture/invalid-log" 2>&1; then
    die 'Corrupted update was accepted.'
fi
[[ $(cat "$ROOT/app/VERSION") == "$original_version" && ! -f $VERSION_REQUEST ]]
printf 'PASS corrupted update rejected without changing installed code\n'
# Even a checksum-valid archive must not replace a working app if a required
# module is missing and its entrypoint cannot pass --check.
sed -i '/^lib\/runtime.sh$/d' "$fixture/app-files.txt"
rm "$fixture/lib/runtime.sh"
bash "$fixture/build.sh"
if (versions_prepare '{"version":"1.0.1","sha":"1111111111111111111111111111111111111111"}') >"$fixture/incomplete-log" 2>&1; then
    die 'Incomplete application was accepted.'
fi
[[ $(cat "$ROOT/app/VERSION") == "$original_version" && ! -f $VERSION_REQUEST ]]
printf 'PASS incomplete application rejected before activation\n'
