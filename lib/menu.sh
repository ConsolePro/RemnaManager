#!/usr/bin/env bash
# Strict child processes stop failed operations; menus stay available.
operation() {
    bash "$APP_DIR/manager.sh" --operation "$@"
    local status=$?
    if ((status)); then printf '\nОперация не завершена (код %s). Настройки и журнал сохранены.\n' "$status" >&2; fi
    return 0
}
cli_menu() {
    while :; do
        menu 'Remnawave CLI' '1. Статус компонентов' '2. Показать доступ к панели' '3. Резервная копия БД' '4. Сменить API-токен' '5. Запустить панель' '6. Штатный CLI в контейнере' '0. Назад'
        case $CHOICE in
            0) return;; 1) operation service panel ps;; 2) operation credentials;; 3) operation backup;;
            4) operation token_change;; 5) operation service panel up -d --wait --wait-timeout 180;;
            6) operation service panel exec remnawave cli;;
        esac
    done
}
panel_menu() {
    while :; do
        menu 'Панель и компоненты' '1. Установить панель' '2. Обновить панель' '3. Остановить панель' '4. Логи панели' '5. Remnawave CLI' '0. Назад'
        case $CHOICE in
            0) return;; 1) operation panel_install;; 2) operation panel_update;; 3) operation service panel stop;;
            4) operation service panel logs --tail 100 --no-color;; 5) cli_menu;;
        esac
    done
}
sub_menu() {
    while :; do
        menu 'Страница подписки' '1. Установка / запуск' '2. Обновление' '3. Кастомизация' '4. Остановить' '0. Назад'
        case $CHOICE in 0) return;; 1) operation sub_install;; 2) operation sub_update;; 3) operation sub_custom;; 4) operation service sub stop;; esac
    done
}
node_menu() {
    local uuid=$1 node title toggle
    while :; do
        if ! node=$(api GET "/nodes/$uuid"); then printf 'Не удалось получить ноду.\n' >&2; return; fi
        title=$(jq -r '.name + " (" + .address + ")"' <<<"$node")
        toggle=$(jq -r 'if .isDisabled or (.isConnected|not) then "Запустить" else "Остановить" end' <<<"$node")
        menu "$title" '1. Шаблон selfsteal' '2. Обновить ядро Xray' '3. Переименовать' '4. Переустановить' '5. Обновить ноду' "6. $toggle" '7. Перезапустить контейнер и Xray' '8. Статус и последние логи' '9. Изменить SSH-подключение' '0. Назад'
        case $CHOICE in
            0) return;; 1) operation node_action "$uuid" template;; 2) operation node_action "$uuid" xray;;
            3) operation node_action "$uuid" rename;; 4) operation node_action "$uuid" install;;
            5) operation node_action "$uuid" update;; 6) operation node_action "$uuid" toggle;;
            7) operation node_action "$uuid" restart;; 8) operation node_action "$uuid" status;; 9) operation node_action "$uuid" ssh;;
        esac
    done
}
nodes_menu() {
    local list item index uuid
    local -a entries
    while :; do
        if ! list=$(nodes); then printf 'Не удалось получить ноды.\n' >&2; return; fi
        entries=(); index=1
        while IFS= read -r item; do
            entries+=("$index. $(jq -r '.name + " (" + .address + ", " + (if .isDisabled then "остановлена" elif .isConnected then "online" else "offline" end) + ")"' <<<"$item")")
            ((index+=1))
        done < <(jq -c '.[]' <<<"$list")
        menu 'Ноды и расширения' "${entries[@]}" 'a. Добавить ноду' 'p. Незавершённые установки' '0. Назад'
        case $CHOICE in
            0) return;; a) operation node_add;; p) operation pending;;
            *)
                if [[ $CHOICE =~ ^[1-9][0-9]{0,5}$ ]] && ((CHOICE < index)); then
                    uuid=$(jq -er --argjson index "$((CHOICE-1))" '.[$index].uuid' <<<"$list")
                    node_menu "$uuid"
                fi;;
        esac
    done
}
warp_menu() {
    while :; do
        menu 'WARP Native' '1. Установить WARP Native на ноду' '2. Удалить WARP Native' '3. Добавить маршруты' '4. Удалить маршруты' '0. Назад'
        case $CHOICE in 0) return;; 1) operation warp_action install;; 2) operation warp_action remove;; 3) operation warp_action enable;; 4) operation warp_action disable;; esac
    done
}
checker_menu() {
    while :; do
        if [[ -f $CHECKER/compose.yaml ]]; then
            menu 'Мониторинг нод' '1. Статус и последние логи' '2. Перезапустить' '3. Обновить образы' '4. Настроить Telegram' '5. Удалить' '0. Назад'
            case $CHOICE in
                0) return;; 1) operation checker_status;; 2) operation service checker restart;;
                3) operation checker_update;; 4) operation telegram;; 5) operation checker_remove;;
            esac
        else
            menu 'Мониторинг нод' '1. Установить Xray Checker' '0. Назад'
            case $CHOICE in 0) return;; 1) operation checker_install;; esac
        fi
    done
}
plugins_menu() {
    while :; do
        menu 'Плагины ноды' '1. Включить BBR + fq' '2. Диагностика и логи' '0. Назад'
        case $CHOICE in 0) return;; 1) operation plugin_action bbr;; 2) operation plugin_action status;; esac
    done
}
main_menu() {
    while :; do
        menu 'RemnaManager' '1. Панель и компоненты' '2. Ноды и расширения' '3. Страница подписки' '4. WARP Native' '5. Мониторинг нод' '6. Плагины' '0. Выход'
        case $CHOICE in 0) return;; 1) panel_menu;; 2) nodes_menu;; 3) sub_menu;; 4) warp_menu;; 5) checker_menu;; 6) plugins_menu;; esac
    done
}
service() {
    local directory
    case $1 in panel) directory=$PANEL;; sub) directory=$SUB;; checker) directory=$CHECKER;; *) die 'Неизвестный компонент.';; esac
    shift
    compose "$directory" "$@"
}
checker_status() { compose "$CHECKER" ps; compose "$CHECKER" logs --tail 100 --no-color; }
