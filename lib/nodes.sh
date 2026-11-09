#!/usr/bin/env bash
node_add() {
    local name address ssh_port cover journal
    name=$(ask 'Имя ноды (3–30 символов)')
    [[ ${#name} -ge 3 && ${#name} -le 30 ]] || die 'Имя должно содержать 3–30 символов.'
    address=$(host "$(ask 'Публичный IP или домен ноды')")
    ssh_port=$(port "$(ask 'SSH порт' 22)")
    cover=$(domain "$(ask 'Домен сайта-заглушки (DNS должен указывать на ноду)')")
    [[ $address != "$(state_get '.panel.ip')" && $cover != "$(state_get '.panel.domain')" && $cover != "$(state_get '.panel.sub_domain')" ]] || die 'Нужны отдельная нода и отдельный домен заглушки.'
    journal=$ROOT/pending/$(random_hex 8).json
    jq -n --arg name "$name" --arg address "$address" --argjson port "$ssh_port" --arg domain "$cover" --arg email "$(state_get '.panel.email')" --arg ip "$(state_get '.panel.ip')" '{name:$name,address:$address,ssh_host:$address,ssh_port:$port,domain:$domain,email:$email,panel_ip:$ip}' | save "$journal"
    finish_node "$journal"
}
finish_node() {
    local journal=$1 data response key config uuid profile inbound attempt field
    [[ -f $journal ]] || die 'Журнал установки не найден.'
    data=$(cat "$journal")
    ssh_deploy "$data"
    if [[ -z $(jq -r '.secret // empty' "$journal") ]]; then
        response=$(api GET /keygen)
        field=$(jq -er '.secretKey | select(type == "string" and length > 0)' <<<"$response")
        json_edit "$journal" '.secret = $secret' --arg secret "$field"
    fi
    data=$(cat "$journal")
    ssh_job "$data" "$(jq '{action:"install",secret,domain,email,panel_ip}' <<<"$data")" no-deploy
    if [[ -z $(jq -r '.profile // empty' "$journal") ]]; then
        key=$(openssl genpkey -algorithm X25519 -outform DER | tail -c 32 | openssl base64 -A | tr '+/' '-_' | tr -d '=')
        config=$(reality_config "$(json_get "$journal" .domain)" "$key" "$(random_hex 8)")
        response=$(api POST /config-profiles "$(jq -n --arg name "RM_$(random_hex 6)" --argjson config "$config" '{name:$name,config:$config}')")
        field=$(jq -er .uuid <<<"$response"); valid_uuid "$field"
        json_edit "$journal" '.profile = $uuid' --arg uuid "$field"
    fi
    profile=$(json_get "$journal" .profile); valid_uuid "$profile"
    if [[ -z $(jq -r '.inbound // empty' "$journal") ]]; then
        response=$(api GET "/config-profiles/$profile/inbounds")
        inbound=$(jq -er '[.inbounds[] | select(.tag == "VLESS_REALITY")][0].uuid // empty' <<<"$response")
        valid_uuid "$inbound"
        json_edit "$journal" '.inbound = $uuid' --arg uuid "$inbound"
    fi
    inbound=$(json_get "$journal" .inbound); valid_uuid "$inbound"
    if [[ -z $(jq -r '.uuid // empty' "$journal") ]]; then
        response=$(api POST /nodes "$(jq '{name,address,port:2222,configProfile:{activeConfigProfileUuid:.profile,activeInbounds:[.inbound]}}' "$journal")")
        field=$(jq -er .uuid <<<"$response"); valid_uuid "$field"
        json_edit "$journal" '.uuid = $uuid' --arg uuid "$field"
    fi
    uuid=$(json_get "$journal" .uuid); valid_uuid "$uuid"
    if [[ -z $(jq -r '.host_uuid // empty' "$journal") ]]; then
        response=$(api POST /hosts "$(jq '{remark:.name,address,port:443,sni:.domain,fingerprint:"chrome",nodes:[.uuid],inbound:{configProfileUuid:.profile,configProfileInboundUuid:.inbound}}' "$journal")")
        field=$(jq -er .uuid <<<"$response"); valid_uuid "$field"
        json_edit "$journal" '.host_uuid = $uuid' --arg uuid "$field"
    fi
    if [[ -z $(jq -r '.squad // empty' "$journal") ]]; then
        response=$(api POST /internal-squads "$(jq '{name:("RM_" + .uuid[0:8]),inbounds:[.inbound]}' "$journal")")
        field=$(jq -er .uuid <<<"$response"); valid_uuid "$field"
        json_edit "$journal" '.squad = $uuid' --arg uuid "$field"
    fi
    state_edit '.nodes[$uuid] = $node' --arg uuid "$uuid" --argjson node "$(jq 'del(.secret)' "$journal")"
    for ((attempt=0;attempt<20;attempt++)); do
        response=$(api GET "/nodes/$uuid")
        if [[ $(jq -r .isConnected <<<"$response") == true ]]; then
            rm -f -- "$journal"
            printf 'Нода подключена. Созданы профиль RAW + Reality, хост и internal squad.\nНазначьте squad пользователям; используется Vision.\n'; return
        fi
        sleep 3
    done
    die 'Нода создана, но offline. Проверьте панель→нода:2222. Журнал сохранён в «Незавершённые установки».'
}
pending() {
    local path index=1
    local -a paths=() entries=()
    shopt -s nullglob
    for path in "$ROOT"/pending/*.json; do paths+=("$path"); entries+=("$index. $(json_get "$path" .name)"); ((index+=1)); done
    if ((${#paths[@]} == 0)); then printf 'Незавершённых установок нет.\n'; return; fi
    menu 'Незавершённые установки' "${entries[@]}" '0. Назад'
    if [[ $CHOICE =~ ^[1-9][0-9]{0,5}$ ]] && ((CHOICE <= ${#paths[@]})); then finish_node "${paths[CHOICE-1]}"; fi
}
select_node() {
    local list item index=1
    local -a entries=()
    list=$(nodes)
    while IFS= read -r item; do
        entries+=("$index. $(jq -r '.name + " (" + .address + ", " + (if .isDisabled then "остановлена" elif .isConnected then "online" else "offline" end) + ")"' <<<"$item")")
        ((index+=1))
    done < <(jq -c '.[]' <<<"$list")
    menu 'Ноды панели' "${entries[@]}" 'a. Добавить ноду' '0. Назад'
    if [[ $CHOICE == a ]]; then node_add >&2; printf 'null\n'; return; fi
    if [[ $CHOICE =~ ^[1-9][0-9]{0,5}$ ]] && ((CHOICE < index)); then jq -c --argjson index "$((CHOICE-1))" '.[$index]' <<<"$list"
    else printf 'null\n'; fi
}
node_action() {
    local uuid=$1 action=$2 node stored name cover secret job path style stopped address ssh_port
    valid_uuid "$uuid"
    node=$(api GET "/nodes/$uuid")
    case $action in
        template)
            menu 'Сайт-заглушка' '1. Студия' '2. Журнал' '3. Мастерская' '4. Свой HTML-файл' '0. Назад'
            case $CHOICE in
                1) style=studio;; 2) style=journal;; 3) style=workshop;;
                4)
                    path=$(ask 'Путь к index.html на управляющем сервере')
                    [[ -f $path && $(wc -c <"$path") -le 2097152 ]] || die 'Нужен HTML-файл до 2 МБ.'
                    job=$(jq -n --rawfile html "$path" '{action:"template",html:$html}');;
                *) return;;
            esac
            if [[ -n ${style:-} ]]; then job=$(jq -n --arg style "$style" '{action:"template",style:$style}'); fi
            ssh_job "$(connection "$node")" "$job";;
        rename)
            name=$(ask 'Новое имя')
            [[ ${#name} -ge 3 && ${#name} -le 30 ]] || die 'Имя: 3–30 символов.'
            api PATCH /nodes "$(jq -n --arg uuid "$uuid" --arg name "$name" '{uuid:$uuid,name:$name}')" >/dev/null;;
        install)
            stored=$(connection "$node")
            cover=$(domain "$(ask 'Домен selfsteal' "$(jq -r '.domain // empty' <<<"$stored")")")
            [[ -z $(jq -r '.domain // empty' <<<"$stored") || $cover == "$(jq -r .domain <<<"$stored")" ]] || die 'При переустановке сохраните домен профиля.'
            secret=$(api GET /keygen | jq -er .secretKey)
            job=$(jq -n --arg domain "$cover" --arg secret "$secret" --arg ip "$(state_get '.panel.ip')" --arg email "$(state_get '.panel.email')" '{action:"install",domain:$domain,secret:$secret,panel_ip:$ip,email:$email}')
            ssh_job "$stored" "$job"
            state_edit '.nodes[$uuid] += $settings' --arg uuid "$uuid" --argjson settings "$(jq 'del(.action,.secret)' <<<"$job")";;
        toggle)
            stored=$(connection "$node")
            stopped=$(jq -r '.isDisabled or (.isConnected|not)' <<<"$node")
            if [[ $stopped == true ]]; then ssh_job "$stored" '{"action":"start"}'; api POST "/nodes/$uuid/actions/enable" >/dev/null
            else api POST "/nodes/$uuid/actions/disable" >/dev/null; ssh_job "$stored" '{"action":"stop"}'; fi;;
        ssh)
            address=$(host "$(ask 'SSH IP/домен' "$(jq -er .address <<<"$node")")")
            ssh_port=$(port "$(ask 'SSH порт' 22)")
            state_edit '.nodes[$uuid] = ((.nodes[$uuid] // {}) + {ssh_host:$host,ssh_port:$port})' --arg uuid "$uuid" --arg host "$address" --argjson port "$ssh_port";;
        xray|update|restart|status)
            ssh_job "$(connection "$node")" "$(jq -n --arg action "$action" '{action:$action}')";;
        *) die 'Неизвестное действие ноды.';;
    esac
}
warp_route() {
    local node=$1 enabled=$2 uuid stored profile response config domains='[]' raw item transport
    uuid=$(jq -er .uuid <<<"$node"); valid_uuid "$uuid"
    stored=$(jq -c --arg uuid "$uuid" '.nodes[$uuid] // {}' "$STATE")
    profile=$(jq -er '.profile // empty' <<<"$stored")
    valid_uuid "$profile"
    [[ $(jq -r '.configProfile.activeConfigProfileUuid' <<<"$node") == "$profile" ]] || die 'Профиль ноды изменён через панель. Изменение маршрутов остановлено.'
    response=$(api GET "/config-profiles/$profile")
    [[ $(jq --arg uuid "$uuid" '[.nodes[]? | select(.uuid != $uuid)] | length' <<<"$response") == 0 ]] || die 'Профиль используется несколькими нодами.'
    if [[ $enabled == true ]]; then
        raw=$(ask 'Домены через WARP, через запятую')
        local -a values
        IFS=, read -r -a values <<<"$raw"
        for item in "${values[@]}"; do item=$(domain "$item"); domains=$(jq --arg item "$item" '. + [$item]' <<<"$domains"); done
        transport=$(connection "$node"); ssh_deploy "$transport"
        ssh_command "bash -o pipefail -c 'curl --fail --silent --max-time 15 --socks5-hostname 127.0.0.1:40000 https://www.cloudflare.com/cdn-cgi/trace | grep -q \"^warp=on\"'"
    fi
    config=$(jq '.config' <<<"$response" | warp_config "$enabled" "$domains")
    printf '%s\n' "$response" | save "$ROOT/backups/profile-$profile-$(date +%s)-$$.json"
    api PATCH /config-profiles "$(jq -n --arg uuid "$profile" --argjson config "$config" '{uuid:$uuid,config:$config}')" >/dev/null
    state_edit '.nodes[$uuid].warp = $enabled' --arg uuid "$uuid" --argjson enabled "$enabled"
    printf 'Профиль обновлён; WARP применяется к выбранным доменам.\n'
}
warp_action() {
    local action=$1 node uuid
    node=$(select_node)
    [[ $node != null ]] || return 0
    uuid=$(jq -er .uuid <<<"$node")
    case $action in
        install) if confirm 'Установка WARP подразумевает принятие условий Cloudflare. Продолжить?'; then ssh_job "$(connection "$node")" '{"action":"warp-install"}'; fi;;
        remove)
            if [[ $(jq -r --arg uuid "$uuid" '.nodes[$uuid].warp // false' "$STATE") == true ]]; then warp_route "$node" false; fi
            ssh_job "$(connection "$node")" '{"action":"warp-remove"}';;
        enable) warp_route "$node" true;;
        disable) warp_route "$node" false;;
    esac
}
plugin_action() {
    local node
    node=$(select_node)
    [[ $node != null ]] || return 0
    ssh_job "$(connection "$node")" "$(jq -n --arg action "$1" '{action:$action}')"
}
