#!/usr/bin/env bash
set -Eeuo pipefail
shopt -s inherit_errexit
umask 077
APP_DIR=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)
source "$APP_DIR/lib/runtime.sh"
source "$APP_DIR/lib/render.sh"
source "$APP_DIR/lib/provision.sh"
source "$APP_DIR/lib/ssh.sh"
source "$APP_DIR/lib/panel.sh"
source "$APP_DIR/lib/nodes.sh"
source "$APP_DIR/lib/monitor.sh"
source "$APP_DIR/lib/menu.sh"
source "$APP_DIR/lib/install.sh"
source "$APP_DIR/lib/versions.sh"
source "$APP_DIR/lib/ui.sh"
if [[ ${1:-} == --version ]]; then current_version; exit 0; fi
if [[ ${1:-} == --check ]]; then
    for file in "$APP_DIR"/*.sh "$APP_DIR"/lib/*.sh; do bash -n "$file"; done
    need jq; need curl; need openssl; need tar; need flock
    printf 'Shell-синтаксис и основные зависимости: OK. Компоненты не устанавливались.\n'
    exit 0
fi
[[ $(uname -s) == Linux && $(id -u) == 0 ]] || die 'Нужны Linux и запуск от root.'
mkdir -p "$ROOT"
chmod 700 "$ROOT"
if [[ ${1:-} == --operation ]]; then
    shift
    case ${1:-} in
        panel_install|panel_update|credentials|backup|token_change|sub_install|sub_update|sub_custom|node_add|pending|node_action|warp_action|checker_install|checker_update|checker_status|telegram|checker_remove|plugin_action|service|versions_prepare|versions_prepare_rollback)
            action=$1; shift
            need jq
            "$action" "$@";;
        *) die 'Неизвестная операция.';;
    esac
    exit 0
fi
case ${1:-} in ''|--notify) ;; *) die 'Использование: bash manager.sh [--check|--notify|--version]';; esac
lock=manager
[[ ${1:-} != --notify ]] || lock=notify
exec 9>"$ROOT/$lock.lock"
flock -n 9 || die 'Другой экземпляр RemnaManager уже работает.'
bootstrap
if [[ ! -f $STATE ]]; then printf '{"nodes":{}}\n' | save "$STATE"; fi
jq -e 'type == "object" and (.nodes|type == "object")' "$STATE" >/dev/null || die 'Некорректный state.json. Восстановите резервную копию.'
if [[ ${1:-} == --notify ]]; then notify; exit; fi
install_commands
if [[ -f $VERSION_REQUEST ]]; then
    printf 'Предыдущее обновление не завершено. Выберите версию повторно в пункте 7.\n'
    rm -f -- "$VERSION_REQUEST"
fi
printf 'Повторный запуск: remna_manager. Сокращение rm без аргументов — в новой Bash-сессии root.\n'
if [[ -f $ROOT/telegram.json && -f /etc/systemd/system/remnamanager-monitor.service ]] && grep -q 'python3' /etc/systemd/system/remnamanager-monitor.service; then monitor_units; fi
set +e
main_menu
if [[ -f $VERSION_REQUEST ]]; then
    set -e
    versions_activate
    exec 9>&-
    exec bash "$ROOT/app/manager.sh"
fi
