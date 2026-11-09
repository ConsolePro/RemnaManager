#!/usr/bin/env bash
set -Eeuo pipefail
cd -- "$(dirname -- "$0")/.."
APP_DIR=$PWD
source lib/runtime.sh
source lib/ui.sh
frames=0
versions_check_cached() { :; }
dashboard() { ((frames+=1)); printf '\033[2J\033[HFRAME %s\n' "$frames" >&2; }
main_screen_read
[[ $CHOICE == 7 && $frames -ge 2 ]]
