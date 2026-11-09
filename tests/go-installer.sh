#!/usr/bin/env bash
# Only run in a disposable supported Linux container.
set -Eeuo pipefail
[[ ${RM_CONTAINER_TESTS:-0} == 1 ]] || { echo 'Set RM_CONTAINER_TESTS=1 in a disposable container.' >&2; exit 1; }
source_dir=$(cd -- "$(dirname -- "$0")/.." && pwd)
bash "$source_dir/install.sh" --source "$source_dir"
remna --version | grep -q 'Go TUI preview'
test "$(readlink /usr/local/bin/re)" = remna
test "$(readlink /usr/local/bin/remna_manager)" = remna
printf '%s\n' '{"language":"en","email":"admin@example.com","initialized":true,"future":42}' >/etc/remnamanager/config.json
chmod 600 /etc/remnamanager/config.json
before=$(sha256sum /etc/remnamanager/config.json)
bash "$source_dir/install.sh" --source "$source_dir"
test "$before" = "$(sha256sum /etc/remnamanager/config.json)"
test "$(stat -c %a /etc/remnamanager/config.json)" = 600
test "$(stat -c %a /etc/remnamanager)" = 700
remna_manager --version
re --version
if bash "$source_dir/install.sh" --ref '..'; then echo 'Invalid ref accepted' >&2; exit 1; fi
python3 "$source_dir/tests/terminal-smoke.py"
