#!/usr/bin/env bash
# Only run in a disposable supported Linux container.
set -Eeuo pipefail
[[ ${RM_CONTAINER_TESTS:-0} == 1 ]] || { echo 'Set RM_CONTAINER_TESTS=1 in a disposable container.' >&2; exit 1; }
source_dir=$(cd -- "$(dirname -- "$0")/.." && pwd)
bash "$source_dir/install.sh" --source "$source_dir"
remanager --version | grep -q 'Go TUI preview'
test "$(readlink /usr/local/bin/re)" = remanager
test "$(readlink /usr/local/bin/remanager)" = ../lib/remnamanager/remanager
# A pre-existing panel command must survive manager reinstallation.
printf '#!/bin/sh\nprintf "panel-cli\\n"\n' >/usr/local/bin/remna
chmod 755 /usr/local/bin/remna
panel_before=$(sha256sum /usr/local/bin/remna)
printf '%s\n' '{"language":"en","email":"admin@example.com","initialized":true,"future":42}' >/etc/remnamanager/config.json
chmod 600 /etc/remnamanager/config.json
before=$(sha256sum /etc/remnamanager/config.json)
bash "$source_dir/install.sh" --source "$source_dir"
test "$before" = "$(sha256sum /etc/remnamanager/config.json)"
test "$(stat -c %a /etc/remnamanager/config.json)" = 600
test "$(stat -c %a /etc/remnamanager)" = 700
test "$panel_before" = "$(sha256sum /usr/local/bin/remna)"
remanager --version
re --version
# Migrate the previous Go command by its build metadata, preserving a backup.
cp /usr/local/lib/remnamanager/remanager /usr/local/bin/remna
ln -sfn remna /usr/local/bin/remna_manager
bash "$source_dir/install.sh" --source "$source_dir"
test ! -e /usr/local/bin/remna
test -x /usr/local/lib/remnamanager/remna.previous
test "$(readlink /usr/local/bin/remna_manager)" = remanager
test "$before" = "$(sha256sum /etc/remnamanager/config.json)"
re --version
if bash "$source_dir/install.sh" --ref '..'; then echo 'Invalid ref accepted' >&2; exit 1; fi
python3 "$source_dir/tests/terminal-smoke.py"
python3 "$source_dir/tests/manager-remove-fixture.py"
