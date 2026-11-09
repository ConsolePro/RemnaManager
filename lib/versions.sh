#!/usr/bin/env bash
VERSION_CACHE=$ROOT/cache/versions.json
VERSION_REQUEST=$ROOT/update-request.json
version_valid() { [[ $1 =~ ^[0-9]{1,5}\.[0-9]{1,5}\.[0-9]{1,5}$ ]]; }
current_version() {
    local value
    if [[ -r $APP_DIR/VERSION ]]; then
        IFS= read -r value <"$APP_DIR/VERSION"
        if version_valid "$value"; then printf '%s\n' "$value"; return; fi
    fi
    printf 'неизвестна\n'
}
version_newer() {
    version_valid "$1" || return 1
    version_valid "$2" || return 1
    local a b i
    local -a left right
    IFS=. read -r -a left <<<"$1"
    IFS=. read -r -a right <<<"$2"
    for i in 0 1 2; do
        a=$((10#${left[i]})); b=$((10#${right[i]}))
        ((a <= b)) || return 0
        ((a >= b)) || return 1
    done
    return 1
}
versions_refresh() {
    local response old='{}' now tags cache
    now=$(date +%s)
    if [[ -f $VERSION_CACHE ]] && jq -e 'type=="object"' "$VERSION_CACHE" >/dev/null 2>&1; then old=$(cat "$VERSION_CACHE"); fi
    if response=$(curl --fail --silent --show-error --location --proto '=https' --proto-redir '=https' --connect-timeout 2 --max-time 4 --max-filesize 1048576 -H 'Accept: application/vnd.github+json' -H 'User-Agent: RemnaManager' 'https://api.github.com/repos/OverlayNode/RemnaManager/tags?per_page=100' 2>/dev/null) &&
        tags=$(jq -ce 'if type!="array" then error("invalid tags") else [.[] | select((.name|test("^v[0-9]{1,5}\\.[0-9]{1,5}\\.[0-9]{1,5}$")) and (.commit.sha|test("^[a-f0-9]{40}$"))) | {version:(.name|ltrimstr("v")),tag:.name,sha:.commit.sha}] | sort_by(.version|split(".")|map(tonumber)) | reverse end' <<<"$response"); then
        cache=$(jq -n --argjson now "$now" --argjson tags "$tags" '{last_attempt:$now,last_success:$now,ok:true,tags:$tags}')
        printf '%s\n' "$cache" | save "$VERSION_CACHE" || return 1
        return 0
    fi
    cache=$(jq --argjson now "$now" '. + {last_attempt:$now,ok:false}' <<<"$old") || return 1
    printf '%s\n' "$cache" | save "$VERSION_CACHE" || return 1
    return 1
}
versions_check_cached() {
    local last=0 now
    if [[ -f $VERSION_CACHE ]]; then last=$(jq -r '.last_attempt // 0' "$VERSION_CACHE" 2>/dev/null) || last=0; fi
    [[ $last =~ ^[0-9]{1,12}$ ]] || last=0
    now=$(date +%s)
    if ((now-last >= 900 || now < last)); then versions_refresh || true; fi
}
version_notice() {
    local latest current
    current=$(current_version)
    latest=''
    if [[ -f $VERSION_CACHE ]]; then latest=$(jq -r '.tags[0].version // empty' "$VERSION_CACHE" 2>/dev/null); fi
    if version_newer "$latest" "$current"; then
        printf 'Доступно обновление v%s → пункт 7' "$latest"
        if [[ $(jq -r '.ok // false' "$VERSION_CACHE") != true ]]; then printf ' (по сохранённой проверке)'; fi
        return
    fi
    if [[ ! -f $VERSION_CACHE ]] || [[ $(jq -r '.ok // false' "$VERSION_CACHE" 2>/dev/null) != true ]]; then
        printf 'Обновления: проверка недоступна · пункт 7 — повторить'
        return
    fi
    if [[ -z $latest ]]; then printf 'Обновления: опубликованных версий пока нет'
    elif [[ $latest == "$current" ]]; then printf 'Установлена актуальная версия'
    else printf 'Последняя опубликованная версия: v%s' "$latest"; fi
}
bundle_extract() {
    local bundle=$1 destination=$2 digest file member listing actual expected
    local archive=$destination/payload.tar
    mkdir -p "$destination/app"
    sed '1,/^__REMNAMANAGER_PAYLOAD__$/d' "$bundle" | base64 -d >"$archive"
    digest=$(sha256sum "$archive"); digest=${digest%% *}
    grep -Fq "'$digest'" "$bundle" || die 'SHA256 установщика не совпадает.'
    listing=$(tar -tf "$archive")
    while IFS= read -r member; do
        [[ $member =~ ^[a-zA-Z0-9_./-]+$ && $member != /* && $member != .. && $member != ../* && $member != */../* && $member != */.. ]] || die 'Недопустимый путь в архиве обновления.'
    done <<<"$listing"
    tar -tvf "$archive" | awk 'substr($0,1,1)!="-"{exit 1}' || die 'Архив должен содержать только обычные файлы.'
    tar --no-same-owner -xf "$archive" -C "$destination/app"
    [[ -f $destination/app/app-files.txt && -f $destination/app/VERSION && -f $destination/app/manager.sh ]] || die 'В архиве нет файлов менеджера.'
    actual=$(printf '%s\n' "$listing" | LC_ALL=C sort)
    expected=$(LC_ALL=C sort "$destination/app/app-files.txt")
    [[ $actual == "$expected" ]] || die 'Состав архива не совпадает с манифестом.'
    while IFS= read -r file; do
        [[ $file != *.sh ]] || bash -n "$destination/app/$file"
    done <"$destination/app/app-files.txt"
}
versions_prepare() {
    local selected=$1 sha version stage actual
    version=$(jq -er .version <<<"$selected")
    sha=$(jq -er .sha <<<"$selected")
    version_valid "$version" || die 'Некорректная версия.'
    [[ $sha =~ ^[a-f0-9]{40}$ ]] || die 'Некорректный commit версии.'
    if ! confirm "Установить RemnaManager v$version? Текущая версия будет сохранена для отката."; then return; fi
    mkdir -p "$ROOT/updates"
    stage=$(mktemp -d "$ROOT/updates/stage.XXXXXXXX")
    download "https://raw.githubusercontent.com/OverlayNode/RemnaManager/$sha/dist/remnamanager.sh" "$stage/installer.sh"
    bundle_extract "$stage/installer.sh" "$stage"
    IFS= read -r actual <"$stage/app/VERSION"
    [[ $actual == "$version" ]] || die 'Версия внутри архива не совпадает с выбранной.'
    bash "$stage/app/manager.sh" --check || die 'Новая версия не прошла проверку запуска. Текущий код сохранён.'
    jq -n --arg stage "$stage" --arg version "$version" '{stage:$stage,version:$version}' | save "$VERSION_REQUEST"
}
versions_prepare_rollback() {
    local snapshot=$1 stage version
    [[ $snapshot =~ ^[a-zA-Z0-9._-]+$ && $snapshot != . && $snapshot != .. && -d $ROOT/versions/$snapshot/app ]] || die 'Копия версии не найдена.'
    version=$(jq -r '.version // "неизвестна"' "$ROOT/versions/$snapshot/metadata.json")
    if ! confirm "Вернуться к сохранённой версии $version? Настройки панели и БД сохранятся."; then return; fi
    mkdir -p "$ROOT/updates"
    stage=$(mktemp -d "$ROOT/updates/stage.XXXXXXXX")
    cp -a -- "$ROOT/versions/$snapshot/app" "$stage/app"
    [[ -f $stage/app/manager.sh ]] || die 'В копии нет менеджера.'
    bash "$stage/app/manager.sh" --check || die 'Сохранённая версия не прошла проверку запуска.'
    jq -n --arg stage "$stage" --arg version "$version" '{stage:$stage,version:$version}' | save "$VERSION_REQUEST"
}
versions_activate() {
    local stage resolved backup old_version='неизвестна' metadata
    stage=$(json_get "$VERSION_REQUEST" .stage)
    resolved=$(realpath -e -- "$stage")
    [[ $resolved == "$ROOT"/updates/stage.* && -d $resolved/app && ! -L $resolved/app ]] || die 'Неверный каталог подготовленной версии.'
    [[ -f $resolved/app/manager.sh ]] || die 'В обновлении нет менеджера.'
    exec 8>"$ROOT/install.lock"
    flock -n 8 || die 'Другой установщик уже работает.'
    if [[ -L $ROOT/app ]]; then die 'Каталог приложения не должен быть символьной ссылкой.'; fi
    if [[ -d $ROOT/app ]]; then
        if [[ -f $ROOT/app/VERSION ]]; then IFS= read -r old_version <"$ROOT/app/VERSION"; fi
        backup=$ROOT/versions/$(date +%Y%m%d-%H%M%S)-$(random_hex 4)
        mkdir -p "$backup"
        metadata=$(jq -n --arg version "$old_version" --arg date "$(date -u +%FT%TZ)" '{version:$version,date:$date}')
        printf '%s\n' "$metadata" | save "$backup/metadata.json"
        mv -- "$ROOT/app" "$backup/app"
    fi
    if ! mv -- "$resolved/app" "$ROOT/app"; then
        if [[ -n ${backup:-} ]]; then mv -- "$backup/app" "$ROOT/app"; fi
        die 'Не удалось установить новую версию; прежний каталог возвращён.'
    fi
    rm -f -- "$VERSION_REQUEST"
    rm -f -- "$resolved/installer.sh" "$resolved/payload.tar"
    rmdir -- "$resolved" 2>/dev/null || true
    flock -u 8
    exec 8>&-
    printf 'Версия менеджера установлена. Перезапуск…\n'
}
versions_menu() {
    local selected selection_mode snapshot directory index
    local -a entries snapshots
    while :; do
        menu "Версии · установлена v$(current_version)" '1. Проверить обновления' '2. Установить последнюю версию' '3. Выбрать опубликованную версию' '4. Вернуться к сохранённой версии' '0. Назад'
        case $CHOICE in
            0) return;;
            1) if versions_refresh; then printf '%s\n' "$(version_notice)"; else printf 'GitHub недоступен. Текущая версия продолжает работать.\n'; fi;;
            2|3)
                selection_mode=$CHOICE
                if ! versions_refresh; then printf 'Не удалось получить версии с GitHub.\n'; continue; fi
                index=$(jq '.tags|length' "$VERSION_CACHE")
                if ((index == 0)); then printf 'Опубликованных версий пока нет.\n'; continue; fi
                if [[ $selection_mode == 2 ]]; then selected=$(jq -c '.tags[0]' "$VERSION_CACHE")
                else
                    entries=()
                    while IFS= read -r selected; do entries+=("$selected"); done < <(jq -r '.tags|to_entries[]|"\(.key+1). v\(.value.version)"' "$VERSION_CACHE")
                    menu 'Опубликованные версии' "${entries[@]}" '0. Назад'
                    [[ $CHOICE =~ ^[1-9][0-9]{0,3}$ ]] || continue
                    ((CHOICE <= index)) || continue
                    selected=$(jq -c --argjson index "$((CHOICE-1))" '.tags[$index]' "$VERSION_CACHE")
                fi
                operation versions_prepare "$selected"
                [[ ! -f $VERSION_REQUEST ]] || return;;
            4)
                entries=(); snapshots=(); index=0
                shopt -s nullglob
                for directory in "$ROOT"/versions/*; do
                    [[ -f $directory/metadata.json && -d $directory/app ]] || continue
                    ((index+=1)); snapshot=${directory##*/}; snapshots+=("$snapshot")
                    entries+=("$index. $(jq -r '.version + " · " + .date' "$directory/metadata.json")")
                done
                if ((index == 0)); then printf 'Сохранённых версий пока нет. Копия появится перед первым обновлением.\n'; continue; fi
                menu 'Сохранённые версии' "${entries[@]}" '0. Назад'
                [[ $CHOICE =~ ^[1-9][0-9]{0,3}$ ]] || continue
                ((CHOICE <= index)) || continue
                operation versions_prepare_rollback "${snapshots[CHOICE-1]}"
                [[ ! -f $VERSION_REQUEST ]] || return;;
        esac
    done
}
