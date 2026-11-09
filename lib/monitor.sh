#!/usr/bin/env bash
checker_install() {
    local url
    url=$(ask 'HTTPS-ссылка подписки отдельного пользователя для мониторинга' '' secret)
    [[ $url == https://?* && ! $url =~ [[:space:]] ]] || die 'Нужна HTTPS-ссылка без пробелов.'
    mkdir -p "$CHECKER"
    checker_compose | save "$CHECKER/compose.yaml"
    env_set "$CHECKER/.env" SUBSCRIPTION_URL "$url"
    checker_update
    wait_http http://127.0.0.1:2112/
    printf 'Xray Checker слушает 127.0.0.1:2112.\n'
}
checker_update() { compose "$CHECKER" pull; compose "$CHECKER" up -d --wait --wait-timeout 120; }
monitor_units() {
    [[ ! $APP_DIR =~ [[:space:]%] ]] || die 'Для systemd разместите проект в пути без пробелов и %.'
    cat <<EOF | save /etc/systemd/system/remnamanager-monitor.service
[Unit]
Description=RemnaManager node state notifications
After=docker.service network-online.target
[Service]
Type=oneshot
ExecStart=/bin/bash $APP_DIR/manager.sh --notify
UMask=0077
EOF
    cat <<'EOF' | save /etc/systemd/system/remnamanager-monitor.timer
[Unit]
Description=Check Remnawave node status every minute
[Timer]
OnBootSec=2min
OnUnitActiveSec=60s
Persistent=true
[Install]
WantedBy=timers.target
EOF
    systemctl daemon-reload
    systemctl enable --now remnamanager-monitor.timer
}
telegram() {
    local token chat
    token=$(ask 'Telegram bot token' '' secret)
    chat=$(ask 'Telegram chat ID')
    [[ $token =~ ^[0-9]+:[A-Za-z0-9_-]+$ && $chat =~ ^-?[0-9]+$ ]] || die 'Неверный token/chat ID.'
    jq -n --arg token "$token" --arg chat "$chat" '{token:$token,chat:$chat}' | save "$ROOT/telegram.json"
    monitor_units
    printf 'Уведомления настроены. Первое состояние — исходное, без сообщения.\n'
}
notify() {
    [[ -f $ROOT/telegram.json ]] || return 0
    local list current previous changes token chat response body config
    list=$(nodes)
    current=$(jq 'map({key:.uuid,value:((.isConnected // false) and ((.isDisabled // false)|not))}) | from_entries' <<<"$list")
    previous='{}'
    if [[ -f $ROOT/monitor-state.json ]]; then previous=$(cat "$ROOT/monitor-state.json"); fi
    changes=$(jq -r --argjson previous "$previous" --argjson current "$current" '[.[] | . as $node | select(($previous|has($node.uuid)) and ($previous[$node.uuid] != $current[$node.uuid])) | .name + ": " + (if $current[.uuid] then "ONLINE" else "OFFLINE" end)] | join("\n")' <<<"$list")
    if [[ -n $changes ]]; then
        token=$(json_get "$ROOT/telegram.json" .token)
        chat=$(json_get "$ROOT/telegram.json" .chat)
        [[ $token =~ ^[0-9]+:[A-Za-z0-9_-]+$ && $chat =~ ^-?[0-9]+$ ]] || die 'Неверная конфигурация Telegram.'
        body=$(jq -n --arg chat "$chat" --arg text "$changes" '{chat_id:$chat,text:$text}')
        config=$(mktemp "$ROOT/telegram-curl.XXXXXXXX")
        printf 'url = "https://api.telegram.org/bot%s/sendMessage"\n' "$token" >"$config"
        if ! response=$(printf '%s' "$body" | curl --config "$config" --fail --silent --connect-timeout 10 --max-time 20 -H 'Content-Type: application/json' --data-binary @-); then
            rm -f "$config"; die 'Telegram не ответил. Повтор при следующей проверке.'
        fi
        rm -f "$config"
        jq -e '.ok == true' <<<"$response" >/dev/null || die 'Telegram отклонил уведомление. Повтор при следующей проверке.'
    fi
    printf '%s\n' "$current" | save "$ROOT/monitor-state.json"
}
checker_remove() {
    if ! confirm 'Удалить Xray Checker и настройки уведомлений?'; then return; fi
    compose "$CHECKER" down
    if [[ -f /etc/systemd/system/remnamanager-monitor.timer ]]; then
        systemctl disable --now remnamanager-monitor.timer
        systemctl stop remnamanager-monitor.service
        rm -f /etc/systemd/system/remnamanager-monitor.timer /etc/systemd/system/remnamanager-monitor.service
        systemctl daemon-reload
    fi
    rm -f -- "$ROOT/telegram.json" "$CHECKER/compose.yaml" "$CHECKER/.env"
    rmdir -- "$CHECKER" 2>/dev/null || true
}
