#!/usr/bin/env bash
set -Eeuo pipefail
shopt -s inherit_errexit
umask 077
APP_DIR=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)
source "$APP_DIR/lib/runtime.sh"
source "$APP_DIR/lib/render.sh"
source "$APP_DIR/lib/provision.sh"
source "$APP_DIR/lib/warp.sh"
[[ $(id -u) == 0 ]] || die 'Нужен запуск от root.'
mkdir -p "$ROOT"
chmod 700 "$ROOT"
exec 9>"$ROOT/agent.lock"
flock -n 9 || die 'Другой агент уже работает.'
JOB=$(cat)
if [[ $JOB == *'"debug"'* ]]; then
    # jq is installed by bootstrap; inspect only after parsing, never eval JSON.
    if command -v jq >/dev/null && [[ $(jq -r '.debug // false' <<<"$JOB") == true ]]; then RM_DEBUG=1; fi
fi
bootstrap
jq -e 'type == "object" and (.action|type == "string")' <<<"$JOB" >/dev/null || die 'Неверное задание агента.'
execute_job "$JOB"
