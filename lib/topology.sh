#!/usr/bin/env bash
# Client injectHosts template and squad coverage adapted from
# eGamesAPI/remnawave-reverse-proxy (MIT). See LICENSES/eGames-MIT.txt.
hosts_list() { api GET /hosts | jq 'if type=="array" then . else .hosts end'; }
list_array() { jq --arg field "$1" 'if type=="array" then . else .[$field] end'; }
pick_many() {
    local list=$1 label=$2 raw item index uuid result='[]'
    local -a entries=() selections
    while IFS= read -r item; do entries+=("$item"); done < <(jq -r 'to_entries[]|"\(.key+1). \(.value.remark // .value.name) (\(.value.address // ""))"' <<<"$list")
    menu "$label" "${entries[@]}" '0. Отмена'
    raw=$CHOICE
    [[ $raw != 0 && -n $raw ]] || { printf '[]\n'; return; }
    raw=${raw//,/ }
    read -r -a selections <<<"$raw"
    for index in "${selections[@]}"; do
        [[ $index =~ ^[1-9][0-9]{0,3}$ ]] || die 'Введите номера через пробел или запятую.'
        ((index <= $(jq length <<<"$list"))) || die 'Неверный номер.'
        uuid=$(jq -er --argjson index "$((index-1))" '.[$index].uuid' <<<"$list")
        valid_uuid "$uuid"
        result=$(jq --arg uuid "$uuid" '. + [$uuid] | unique' <<<"$result")
    done
    printf '%s\n' "$result"
}
host_copy_body() {
    jq '{remark,address,port,path,sni,host,alpn,fingerprint,securityLayer,xhttpExtraParams,muxParams,sockoptParams,finalMask,overrideSniFromAddress,keepSniBlank,pinnedPeerCertSha256,verifyPeerCertByName,vlessRouteId,shuffleHost,mihomoX25519,mihomoIpVersion,xrayJsonTemplateUuid,excludeFromSubscriptionTypes,mapper,
        inbound:{configProfileUuid:.inbound.configProfileUuid,configProfileInboundUuid:.inbound.configProfileInboundUuid},nodes:(.nodes//[]),tags:(.tags//[])} | with_entries(select(.value!=null))'
}
balancer_template() {
    local members=$1
    jq --argjson members "$members" '
      . as $base |
      .dns //= {servers:["1.1.1.1","1.0.0.1"],queryStrategy:"UseIP"} |
      .inbounds //= [{tag:"socks",port:10808,listen:"127.0.0.1",protocol:"socks",settings:{udp:true,auth:"noauth"},sniffing:{enabled:true,routeOnly:false,destOverride:["http","tls","quic"]}}] |
      .outbounds //= [{tag:"direct",protocol:"freedom"},{tag:"block",protocol:"blackhole"}] |
      .outbounds = ([.outbounds[] | select(.tag!="RM_BALANCE_BLOCK")] + [{tag:"RM_BALANCE_BLOCK",protocol:"blackhole"}]) |
      .remnawave = {injectHosts:[{selector:{type:"uuids",values:$members},tagPrefix:"proxy"}]} |
      .observatory = {subjectSelector:["proxy"],probeURL:"https://www.gstatic.com/generate_204",probeInterval:"30s",enableConcurrency:true} |
      del(.burstObservatory) |
      .routing.domainStrategy //= "IPIfNonMatch" |
      .routing.balancers = [{tag:"RM_BALANCE",selector:["proxy"],strategy:{type:"leastPing"},fallbackTag:"RM_BALANCE_BLOCK"}] |
      .routing.rules = ([($base.routing.rules//[])[] | select([keys[]|select(.!="type" and .!="network" and .!="outboundTag" and .!="balancerTag")]|length>0)] + [{network:"tcp,udp",balancerTag:"RM_BALANCE"}])'
}
balancer_create() {
    local all selected name id file source uuid response clone template base='{}' first members='[]' wrapper originals='[]' squads squad inbound_set before after settings
    all=$(hosts_list | jq '[.[]|select(.isDisabled!=true and .isHidden!=true and .xrayJsonTemplateUuid==null and (((.tags//[])|index("RM_BALANCER"))==null))]')
    selected=$(pick_many "$all" 'Выберите ноды для одной записи (номера через пробел)')
    (( $(jq length <<<"$selected") >= 2 )) || die 'Для балансировщика нужны хотя бы две ноды.'
    [[ $(jq --argjson selected "$selected" '[.balancers[]?.sources[]? | select(.uuid as $u | $selected|index($u))]|length' "$STATE") == 0 ]] || die 'Одна из нод уже участвует в другом балансировщике.'
    name=$(ask 'Название записи в подписке (например, Германия)')
    [[ ${#name} -le 100 ]] || die 'Название: не более 100 символов.'
    inbound_set=$(jq --argjson selected "$selected" '[.[]|select(.uuid as $u|$selected|index($u))|.inbound.configProfileInboundUuid]|unique' <<<"$all")
    [[ $(jq --argjson inbounds "$inbound_set" '[.balancers[]?.sources[]?|select(.inbound.configProfileInboundUuid as $i|$inbounds|index($i))]|length' "$STATE") == 0 ]] || die 'Профиль выбранной ноды уже участвует в другом балансировщике.'
    printf 'Балансировщик работает в клиентах Xray JSON. Squads выбранных нод получат доступ ко всему пулу.\n'
    if ! confirm 'Создать одну запись в подписке?'; then return; fi
    id=$(random_hex 8); file=$ROOT/topology/balancer-$id.json
    jq -n --arg name "$name" --argjson selected "$selected" '{name:$name,selected:$selected,members:[],sources:[],squads:[],pending:true}' | save "$file"
    response=$(api GET /subscription-templates)
    uuid=$(jq -r '.templates[]?|select(.templateType=="XRAY_JSON" and .name=="Default")|.uuid' <<<"$response" | head -1)
    if [[ -n $uuid ]]; then base=$(api GET "/subscription-templates/$uuid" | jq '.templateJson // {}'); fi
    response=$(api POST /subscription-templates "$(jq -n --arg name "RM_Balancer_$id" '{name:$name,templateType:"XRAY_JSON"}')")
    template=$(jq -er .uuid <<<"$response"); valid_uuid "$template"
    json_edit "$file" '.template=$uuid' --arg uuid "$template"
    while IFS= read -r uuid; do
        source=$(api GET "/hosts/$uuid")
        originals=$(jq --argjson source "$source" '.+[$source]' <<<"$originals")
        json_edit "$file" '.sources=$sources' --argjson sources "$originals"
        response=$(api POST /hosts "$(host_copy_body <<<"$source" | jq '. + {isHidden:true,isDisabled:false,excludeFromSubscriptionTypes:[],tags:((.tags//[])+["RM_BALANCER"]|unique)}')")
        clone=$(jq -er .uuid <<<"$response"); valid_uuid "$clone"
        members=$(jq --arg uuid "$clone" '.+[$uuid]' <<<"$members")
        json_edit "$file" '.members=$members' --argjson members "$members"
    done < <(jq -r '.[]' <<<"$selected")
    api PATCH /subscription-templates "$(jq -n --arg uuid "$template" --argjson config "$(balancer_template "$members" <<<"$base")" '{uuid:$uuid,templateJson:$config}')" >/dev/null
    first=$(jq '.[0]' <<<"$originals")
    response=$(api POST /hosts "$(host_copy_body <<<"$first" | jq --arg name "$name" --arg template "$template" '. + {remark:$name,isHidden:false,isDisabled:false,vlessRouteId:null,xrayJsonTemplateUuid:$template,tags:["RM_BALANCER"],excludeFromSubscriptionTypes:["XRAY_BASE64","MIHOMO","STASH","CLASH","SINGBOX"]}')")
    wrapper=$(jq -er .uuid <<<"$response"); valid_uuid "$wrapper"
    json_edit "$file" '.wrapper=$uuid' --arg uuid "$wrapper"
    inbound_set=$(jq '[.[].inbound.configProfileInboundUuid]|unique' <<<"$originals")
    squads=$(api GET /internal-squads | list_array internalSquads)
    while IFS= read -r squad; do
        uuid=$(jq -er .uuid <<<"$squad")
        before=$(jq '[.inbounds[]?|if type=="string" then . else .uuid end]' <<<"$squad")
        [[ $(jq --argjson all "$inbound_set" '[.[]|select(. as $i|$all|index($i))]|length' <<<"$before") != 0 ]] || continue
        after=$(jq --argjson all "$inbound_set" '.+$all|unique' <<<"$before")
        json_edit "$file" '.squads += [{uuid:$uuid,before:$before,added:($after-$before)}]' --arg uuid "$uuid" --argjson before "$before" --argjson after "$after"
        api PATCH /internal-squads "$(jq -n --arg uuid "$uuid" --argjson inbounds "$after" '{uuid:$uuid,inbounds:$inbounds}')" >/dev/null
    done < <(jq -c '.[]' <<<"$squads")
    # Hide originals only from JSON; ordinary VLESS/Mihomo subscriptions remain usable.
    while IFS= read -r source; do
        uuid=$(jq -er .uuid <<<"$source")
        api PATCH /hosts "$(jq --arg uuid "$uuid" '{uuid:$uuid,excludeFromSubscriptionTypes:((.excludeFromSubscriptionTypes//[])+["XRAY_JSON"]|unique)}' <<<"$source")" >/dev/null
    done < <(jq -c '.[]' <<<"$originals")
    settings=$(api GET /subscription-settings)
    if [[ -z $(state_get '.balancer_settings') ]]; then state_edit '.balancer_settings=$settings' --argjson settings "$(jq '{uuid,serveJsonAtBaseSubscription:(.serveJsonAtBaseSubscription//false)}' <<<"$settings")"; fi
    api PATCH /subscription-settings "$(jq '{uuid,serveJsonAtBaseSubscription:true}' <<<"$settings")" >/dev/null
    json_edit "$file" '.pending=false'
    state_edit '.balancers[$id]=$balancer' --arg id "$id" --argjson balancer "$(cat "$file")"
    printf 'Балансировщик «%s» создан. Обновите подписку в клиенте Xray JSON.\n' "$name"
}
balancer_remove_file() {
    local file=$1 source uuid squad response current added original last settings
    # Journal is retained until cleanup completes; failed cleanup can be retried.
    if [[ -n $(jq -r '.wrapper//empty' "$file") ]]; then api_delete "/hosts/$(json_get "$file" .wrapper)" >/dev/null; json_edit "$file" 'del(.wrapper)'; fi
    while IFS= read -r uuid; do
        api_delete "/hosts/$uuid" >/dev/null
        json_edit "$file" '.members -= [$uuid]' --arg uuid "$uuid"
    done < <(jq -r '.members[]?' "$file")
    while IFS= read -r source; do
        uuid=$(jq -er .uuid <<<"$source")
        current=$(api GET "/hosts/$uuid")
        original=$(jq '.excludeFromSubscriptionTypes // []' <<<"$source")
        api PATCH /hosts "$(jq --argjson original "$original" '{uuid,excludeFromSubscriptionTypes:((.excludeFromSubscriptionTypes//[]|map(select(.!="XRAY_JSON")))+$original|unique)}' <<<"$current")" >/dev/null
    done < <(jq -c '.sources[]?' "$file")
    while IFS= read -r squad; do
        uuid=$(jq -er .uuid <<<"$squad")
        response=$(api GET "/internal-squads/$uuid")
        added=$(jq .added <<<"$squad")
        current=$(jq --argjson added "$added" '[.inbounds[]?|if type=="string" then . else .uuid end]-$added' <<<"$response")
        api PATCH /internal-squads "$(jq -n --arg uuid "$uuid" --argjson inbounds "$current" '{uuid:$uuid,inbounds:$inbounds}')" >/dev/null
    done < <(jq -c '.squads[]?' "$file")
    if [[ -n $(jq -r '.template//empty' "$file") ]]; then api_delete "/subscription-templates/$(json_get "$file" .template)" >/dev/null; json_edit "$file" 'del(.template)'; fi
    uuid=${file##*/balancer-}; uuid=${uuid%.json}
    last=$(jq --arg id "$uuid" '[.balancers//{}|to_entries[]|select(.key!=$id)]|length' "$STATE")
    if ((last == 0)) && [[ -n $(state_get '.balancer_settings') ]]; then
        settings=$(state_get '.balancer_settings')
        api PATCH /subscription-settings "$settings" >/dev/null
        state_edit 'del(.balancer_settings)'
    fi
}
managed_profile() {
    local node=$1 uuid profile response
    uuid=$(jq -er .uuid <<<"$node"); valid_uuid "$uuid"
    profile=$(jq -er --arg uuid "$uuid" '.nodes[$uuid].profile // empty' "$STATE"); valid_uuid "$profile"
    [[ $(jq -r '.configProfile.activeConfigProfileUuid' <<<"$node") == "$profile" ]] || die 'Выберите ноду с исходным профилем RemnaManager.'
    response=$(api GET "/config-profiles/$profile")
    [[ $(jq --arg uuid "$uuid" '[.nodes[]?|select(.uuid!=$uuid)]|length' <<<"$response") == 0 ]] || die 'Профиль используется несколькими нодами.'
    printf '%s\n' "$response"
}
bridge_config() {
    local outbound=$1 enabled=$2 tags=${3:-'["VLESS_REALITY"]'}
    jq --argjson outbound "$outbound" --argjson enabled "$enabled" --argjson tags "$tags" '
      .outbounds = [(.outbounds//[])[]|select(.tag!="RM_BRIDGE")] |
      .routing.rules = [(.routing.rules//[])[]|select((.ruleTag//""|startswith("RM_BRIDGE"))|not)|select(.outboundTag!="RM_BRIDGE")] |
      if $enabled then
        .outbounds += [$outbound] | .routing.domainStrategy="IPOnDemand" |
        .routing.rules = ([.routing.rules[]|select((.ip//[]|index("geoip:private"))!=null)] +
          [{type:"field",ruleTag:"RM_BRIDGE_RU_DOMAINS",inboundTag:$tags,domain:["geosite:category-ru"],outboundTag:"DIRECT"},
           {type:"field",ruleTag:"RM_BRIDGE_RU_IP",inboundTag:$tags,ip:["geoip:ru"],outboundTag:"DIRECT"}] +
          [.routing.rules[]|select((.ip//[]|index("geoip:private"))==null)|select([keys[]|select(.!="type" and .!="network" and .!="outboundTag" and .!="balancerTag" and .!="ruleTag")]|length>0)] +
          [{type:"field",ruleTag:"RM_BRIDGE_DEFAULT",inboundTag:$tags,network:"tcp,udp",outboundTag:"RM_BRIDGE"}] +
          [.routing.rules[]|select([keys[]|select(.!="type" and .!="network" and .!="outboundTag" and .!="balancerTag" and .!="ruleTag")]|length==0)])
      else . end'
}
reality_public_key() {
    local key=$1 temporary decoded
    [[ $key =~ ^[a-zA-Z0-9_-]{43}$ ]] || die 'Не найден ключ Reality.'
    temporary=$(mktemp "$ROOT/reality-key.XXXXXXXX")
    # PKCS#8 DER prefix for a 32-byte X25519 private key; never write a shell expression.
    printf '\x30\x2e\x02\x01\x00\x30\x05\x06\x03\x2b\x65\x6e\x04\x22\x04\x20' >"$temporary"
    decoded=$(printf '%s=' "$key" | tr '_-' '/+' | openssl base64 -d -A | openssl base64 -A)
    printf '%s' "$decoded" | openssl base64 -d -A >>"$temporary"
    openssl pkey -inform DER -in "$temporary" -pubout -outform DER 2>/dev/null | tail -c 32 | openssl base64 -A | tr '+/' '-_' | tr -d '='
    rm -f "$temporary"
}
bridge_create() {
    local entry exit entry_id exit_id entry_profile exit_profile entry_config reality user response key uuid file outbound transport tags
    printf 'Выберите входную ноду РФ.\n'
    entry=$(select_node); [[ $entry != null ]] || return 0
    printf 'Выберите выходную ноду Европы.\n'
    exit=$(select_node); [[ $exit != null ]] || return 0
    entry_id=$(jq -er .uuid <<<"$entry"); exit_id=$(jq -er .uuid <<<"$exit")
    [[ $entry_id != "$exit_id" ]] || die 'Вход и выход должны быть разными нодами.'
    [[ $(jq --arg id "$entry_id" '.bridges[$id] // empty' "$STATE") == '' ]] || die 'Для входной ноды мост уже настроен.'
    [[ $(jq --arg id "$exit_id" '.bridges[$id] // empty' "$STATE") == '' ]] || die 'Выходная нода уже является входом другого моста.'
    entry_profile=$(managed_profile "$entry"); exit_profile=$(managed_profile "$exit")
    entry_config=$(jq .config <<<"$entry_profile")
    tags=$(jq '[.config.inbounds[]|select(.protocol=="vless" and .streamSettings.security=="reality")|.tag]' <<<"$entry_profile")
    [[ $(jq length <<<"$tags") == 1 ]] || die 'Для моста нужен один Reality-inbound на входной ноде.'
    reality=$(jq -e '[.config.inbounds[]|select(.protocol=="vless" and .streamSettings.security=="reality")|.streamSettings.realitySettings]|if length==1 then .[0] else error("ambiguous Reality inbound") end' <<<"$exit_profile")
    key=$(reality_public_key "$(jq -er .privateKey <<<"$reality")")
    [[ $key =~ ^[a-zA-Z0-9_-]{43}$ ]] || die 'Не удалось получить публичный ключ Reality.'
    if ! confirm 'РФ направлять напрямую, остальное — через выбранную европейскую ноду?'; then return; fi
    file=$ROOT/topology/bridge-$entry_id.json
    jq -n --arg entry "$entry_id" --arg exit "$exit_id" --arg profile "$(jq -er .uuid <<<"$entry_profile")" --argjson before "$entry_config" '{entry:$entry,exit:$exit,profile:$profile,before:$before,pending:true}' | save "$file"
    response=$(api POST /users "$(jq -n --arg name "RM_bridge_$(random_hex 6)" --arg squad "$(jq -er --arg id "$exit_id" '.nodes[$id].squad' "$STATE")" '{username:$name,status:"ACTIVE",trafficLimitBytes:0,expireAt:"2099-12-31T00:00:00.000Z",activeInternalSquads:[$squad],description:"RemnaManager bridge",tag:"RM_BRIDGE"}')")
    uuid=$(jq -er 'if .id!=null then .id|tostring else .uuid end' <<<"$response")
    [[ $uuid =~ ^[0-9]+$ ]] || valid_uuid "$uuid"
    json_edit "$file" '.user=$uuid' --arg uuid "$uuid"
    user=$(jq -er .vlessUuid <<<"$response"); valid_uuid "$user"
    outbound=$(jq -n --arg id "$user" --arg address "$(jq -er .address <<<"$exit")" --arg key "$key" --arg server "$(jq -er '.serverNames[0]' <<<"$reality")" --arg short "$(jq -er '.shortIds[0]' <<<"$reality")" '{tag:"RM_BRIDGE",protocol:"vless",settings:{vnext:[{address:$address,port:443,users:[{id:$id,encryption:"none",flow:"xtls-rprx-vision"}]}]},streamSettings:{network:"raw",security:"reality",realitySettings:{serverName:$server,fingerprint:"chrome",publicKey:$key,shortId:$short}}}')
    # Validate the complete generated config on the entry node before publishing.
    transport=$(connection "$entry")
    ssh_job "$transport" "$(jq -n --argjson config "$(bridge_config "$outbound" true "$tags" <<<"$entry_config")" '{action:"validate-config",config:$config}')"
    api PATCH /config-profiles "$(jq -n --arg uuid "$(jq -er .uuid <<<"$entry_profile")" --argjson config "$(bridge_config "$outbound" true "$tags" <<<"$entry_config")" '{uuid:$uuid,config:$config}')" >/dev/null
    json_edit "$file" '.pending=false'
    state_edit '.bridges[$id]=$bridge' --arg id "$entry_id" --argjson bridge "$(cat "$file")"
    printf 'Мост создан: РФ напрямую, остальные соединения через европейскую ноду.\n'
}
bridge_remove_file() {
    local file=$1 profile config current before
    profile=$(json_get "$file" .profile); valid_uuid "$profile"
    current=$(api GET "/config-profiles/$profile")
    config=$(jq .config <<<"$current" | bridge_config '{}' false)
    before=$(jq '.before.routing.domainStrategy // null' "$file")
    if [[ $(jq -r .routing.domainStrategy <<<"$config") == IPOnDemand ]]; then
        config=$(jq --argjson strategy "$before" 'if $strategy==null then del(.routing.domainStrategy) else .routing.domainStrategy=$strategy end' <<<"$config")
    fi
    api PATCH /config-profiles "$(jq -n --arg uuid "$profile" --argjson config "$config" '{uuid:$uuid,config:$config}')" >/dev/null
    if [[ -n $(jq -r '.user//empty' "$file") ]]; then api_delete "/users/$(json_get "$file" .user)" >/dev/null; json_edit "$file" 'del(.user)'; fi
}
topology_remove() {
    local file index=1 kind id
    local -a files=() entries=()
    shopt -s nullglob
    for file in "$ROOT"/topology/*.json; do
        files+=("$file"); entries+=("$index. $(jq -r '.name // (.entry+" → "+.exit)' "$file")$(jq -r 'if .pending then " [незавершено]" else "" end' "$file")"); ((index+=1))
    done
    if ((${#files[@]}==0)); then printf 'Балансировщиков и мостов нет.\n'; return; fi
    menu 'Удаление или откат незавершённой настройки' "${entries[@]}" '0. Назад'
    [[ $CHOICE =~ ^[1-9][0-9]{0,3}$ ]] || return 0
    ((CHOICE < index)) || return 0
    file=${files[CHOICE-1]}
    if ! confirm 'Удалить эту настройку и вернуть связанные параметры?'; then return; fi
    case ${file##*/} in
        balancer-*) kind=balancers; id=${file##*/balancer-}; id=${id%.json}; balancer_remove_file "$file";;
        bridge-*) kind=bridges; id=$(json_get "$file" .entry); bridge_remove_file "$file";;
    esac
    state_edit 'del(.[$kind][$id])' --arg kind "$kind" --arg id "$id"
    rm -f "$file"
}
topology_menu() {
    while :; do
        menu 'БАЛАНСИРОВЩИКИ И МОСТЫ' '1. Клиентский балансировщик (одна запись)' '2. Мост РФ → Европа' '3. Список, удаление и откат незавершённой настройки' '0. Назад'
        case $CHOICE in 0) return;; 1) operation balancer_create;; 2) operation bridge_create;; 3) operation topology_remove;; esac
    done
}
