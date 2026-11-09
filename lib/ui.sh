#!/usr/bin/env bash
# Dashboard reads local kernel/interface information; no public IP lookup service.
ui_colors() {
    UI_RESET='' UI_BOLD='' UI_CYAN='' UI_GREEN='' UI_YELLOW='' UI_DIM=''
    if [[ -t 2 && ${TERM:-dumb} != dumb && -z ${NO_COLOR:-} ]]; then
        UI_RESET=$'\033[0m' UI_BOLD=$'\033[1m' UI_CYAN=$'\033[36m'
        UI_GREEN=$'\033[32m' UI_YELLOW=$'\033[33m' UI_DIM=$'\033[2m'
    fi
}
ui_line() {
    local width=${COLUMNS:-72} line
    [[ $width =~ ^[0-9]{1,3}$ ]] || width=72
    ((width >= 48)) || width=48
    ((width <= 90)) || width=90
    printf -v line '%*s' "$width" ''
    printf '%s%s%s\n' "$UI_DIM" "${line// /─}" "$UI_RESET" >&2
}
cpu_counters() {
    [[ -r /proc/stat ]] || return 1
    awk '/^cpu / {printf "%.0f %.0f\n", $2+$3+$4+$5+$6+$7+$8+$9, $5+$6; exit}' /proc/stat
}
cpu_usage() {
    local total_a idle_a total_b idle_b delta busy
    read -r total_a idle_a < <(cpu_counters) || { printf 'н/д'; return; }
    sleep 0.15
    read -r total_b idle_b < <(cpu_counters) || { printf 'н/д'; return; }
    delta=$((total_b-total_a)); busy=$((delta-idle_b+idle_a))
    if ((delta <= 0)); then printf '0%%'; return; fi
    ((busy >= 0)) || busy=0
    ((busy <= delta)) || busy=$delta
    printf '%d%%' "$((100*busy/delta))"
}
human_bytes() {
    awk -v bytes="${1:-0}" 'BEGIN {split("Б КиБ МиБ ГиБ ТиБ ПиБ",units," "); i=1; while(bytes>=1024 && i<6){bytes/=1024;i++} printf "%.1f %s",bytes,units[i]}'
}
system_status() {
    local size available percent mount interface route4 route6 state addresses
    STATUS_CORES=$(nproc 2>/dev/null) || STATUS_CORES='н/д'
    STATUS_CPU=$(cpu_usage)
    STATUS_LOAD='н/д'
    if [[ -r /proc/loadavg ]]; then read -r STATUS_LOAD _ </proc/loadavg; fi
    STATUS_DISK='н/д'
    if read -r size available percent mount < <(df -B1 --output=size,avail,pcent,target "$ROOT" 2>/dev/null | awk 'NR==2'); then
        STATUS_DISK="$(human_bytes "$size") всего · $(human_bytes "$available") свободно · $percent занято ($mount)"
    fi
    STATUS_IPV4='нет адреса' STATUS_IPV6='нет адреса' STATUS_NETWORK='н/д'
    if ! command -v ip >/dev/null; then return; fi
    route4=$(ip -4 route show default 2>/dev/null | awk 'NR==1{for(i=1;i<=NF;i++)if($i=="dev"){print $(i+1);exit}}')
    route6=$(ip -6 route show default 2>/dev/null | awk 'NR==1{for(i=1;i<=NF;i++)if($i=="dev"){print $(i+1);exit}}')
    interface=${route4:-$route6}
    if [[ -n $interface ]]; then
        state=$(ip -o link show dev "$interface" 2>/dev/null | awk '{for(i=1;i<=NF;i++)if($i=="state"){print $(i+1);exit}}')
        STATUS_NETWORK="$interface · ${state:-н/д} · маршрут ${route4:+IPv4}${route4:+${route6:+ + }}${route6:+IPv6}"
    else STATUS_NETWORK='нет маршрута по умолчанию'; fi
    # Show addresses on the actual egress interface, excluding Docker bridges.
    local -a args4=() args6=()
    [[ -z ${route4:-$interface} ]] || args4=(dev "${route4:-$interface}")
    [[ -z ${route6:-$interface} ]] || args6=(dev "${route6:-$interface}")
    addresses=$(ip -o -4 addr show "${args4[@]}" scope global 2>/dev/null | awk '{split($4,a,"/"); if(!seen[a[1]]++)printf "%s%s",sep,a[1];sep=", "}')
    [[ -z $addresses ]] || STATUS_IPV4=$addresses
    addresses=$(ip -o -6 addr show "${args6[@]}" scope global 2>/dev/null | awk '!/ tentative| dadfailed/ {split($4,a,"/"); if(!seen[a[1]]++)printf "%s%s",sep,a[1];sep=", "}')
    [[ -z $addresses ]] || STATUS_IPV6=$addresses
}
dashboard() {
    ui_colors
    if [[ -t 2 && ${TERM:-dumb} != dumb ]]; then printf '\033[2J\033[H' >&2; fi
    system_status
    ui_line
    printf '  %s%sRemnaManager%s  %sv%s%s\n' "$UI_BOLD" "$UI_CYAN" "$UI_RESET" "$UI_DIM" "$(current_version)" "$UI_RESET" >&2
    printf '  %sУправление сервером и Remnawave%s\n' "$UI_DIM" "$UI_RESET" >&2
    ui_line
    printf '  ЦП       %s ядер · загрузка %s · load %s\n' "$STATUS_CORES" "$STATUS_CPU" "$STATUS_LOAD" >&2
    printf '  Диск     %s\n  Сеть     %s\n' "$STATUS_DISK" "$STATUS_NETWORK" >&2
    printf '  IPv4     %s\n  IPv6     %s\n' "$STATUS_IPV4" "$STATUS_IPV6" >&2
    ui_line
    printf '  %s%s%s\n' "$UI_YELLOW" "$(version_notice)" "$UI_RESET" >&2
}
ui_menu_render() {
    local title=$1 item key label
    shift
    ui_colors
    printf '\n  %s%s%s\n\n' "$UI_BOLD" "$title" "$UI_RESET" >&2
    for item in "$@"; do
        key=${item%%.*}; label=${item#*. }
        printf '  %s[%s]%s  %s\n' "$UI_CYAN" "$key" "$UI_RESET" "$label" >&2
    done
    printf '\n  %sВыберите действие › %s' "$UI_BOLD" "$UI_RESET" >&2
}
ui_menu() { ui_menu_render "$@"; IFS= read -r CHOICE || CHOICE=0; }
main_screen_render() {
    dashboard
    ui_menu_render 'УПРАВЛЕНИЕ' '1. Панель и компоненты' '2. Ноды и расширения' '3. Страница подписки' '4. WARP-Native' '5. Мониторинг нод' '6. Плагины' '7. Версии и обновления' '8. Балансировщики и мосты' 'r. Обновить показатели' '0. Выход'
}
main_screen_read() {
    local buffer='' key status interval=${RM_REFRESH_INTERVAL:-2}
    versions_check_cached
    main_screen_render
    if [[ ! -t 0 || ! -t 2 || ${TERM:-dumb} == dumb || ${RM_LIVE_OFF:-0} == 1 ]]; then
        IFS= read -r CHOICE || CHOICE=0
        return
    fi
    [[ $interval =~ ^[1-9][0-9]?$ ]] && ((interval <= 30)) || interval=2
    # Read one character at a time: refreshes cannot consume a half-typed choice.
    # No background renderer survives this function or writes over other prompts.
    while :; do
        if IFS= read -r -s -n 1 -t "$interval" key; then status=0; else status=$?; fi
        if ((status == 0)); then
            case $key in
                ''|$'\n'|$'\r') CHOICE=$buffer; printf '\n' >&2; return;;
                $'\177'|$'\b') if [[ -n $buffer ]]; then buffer=${buffer%?}; printf '\b \b' >&2; fi;;
                [0-9rR]) buffer+=$key; printf '%s' "$key" >&2;;
            esac
        elif ((status > 128)); then
            versions_check_cached
            main_screen_render
            printf '%s' "$buffer" >&2
        else CHOICE=0; printf '\n' >&2; return; fi
    done
}
