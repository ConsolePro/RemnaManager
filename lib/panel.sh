#!/usr/bin/env bash
backup() {
    local directory path
    directory=$ROOT/backups/$(date +%Y%m%d-%H%M%S)-$$
    mkdir -p "$directory"
    compose "$PANEL" exec -T remnawave-db sh -c 'exec pg_dump -U "$POSTGRES_USER" -d "$POSTGRES_DB" -Fc' >"$directory/database.dump"
    for path in "$PANEL/.env" "$PANEL/docker-compose.yml" "$STATE"; do
        if [[ -f $path ]]; then cp -p -- "$path" "$directory/"; fi
    done
    printf 'Резервная копия: %s\n' "$directory"
}
credentials() {
    jq -r '.panel | "\nВход: https://\(.domain)/enter-\(.gate)\nЛогин: \(.admin)\nПароль: \(.password)"' "$STATE"
    printf 'Секреты сохранены в %s (root, 0600).\n' "$STATE"
}
create_subscription_token() {
    local token=$1 response subscription_token
    response=$(API_TOKEN=$token API_ADMIN=1 api POST /tokens '{"name":"RemnaManager Subpage","expiresInDays":3650,"scopes":["subscriptions:read","subscription-page-configs:read","system:read"]}')
    subscription_token=$(jq -er '.token | select(type == "string" and length > 0)' <<<"$response")
    state_edit '.subscription_token = $token' --arg token "$subscription_token"
}
panel_install() {
    local name subdomain email ip admin password gate version dbpass staging response auth token manager_token
    if [[ -f $PANEL/docker-compose.yml && -z $(state_get '.panel') ]]; then die 'Найдена чужая установка /opt/remnawave. Перезапись запрещена.'; fi
    if [[ $(state_get '.panel.ready') == true ]]; then
        compose "$PANEL" up -d --wait --wait-timeout 180
        printf 'Панель уже установлена и запущена.\n'
        if [[ $(state_get '.subscription') != true ]] && confirm 'Установить страницу подписки?'; then sub_install preset; fi
        return
    fi
    name=$(domain "$(ask 'Домен панели' "$(state_get '.panel.domain')")")
    subdomain=$(domain "$(ask 'Домен страницы подписки' "$(state_get '.panel.sub_domain')")")
    [[ $name != "$subdomain" ]] || die 'Панели и подписке нужны разные домены.'
    email=$(ask 'Email для ACME' "$(state_get '.panel.email')")
    ip=$(valid_ip "$(ask 'Публичный IP управляющего сервера' "$(state_get '.panel.ip')")")
    admin=$(ask 'Логин администратора' "$(state_get '.panel.admin // "admin"')")
    [[ $admin =~ ^[A-Za-z0-9_]{3,30}$ ]] || die 'Логин: 3–30 латинских букв, цифр или _.'
    if [[ -f $PANEL/.env ]]; then
        [[ $(state_get '.panel.domain') == "$name" && $(state_get '.panel.sub_domain') == "$subdomain" && $(state_get '.panel.admin') == "$admin" ]] || die 'Для продолжения установки сохраните прежние домены и логин.'
    fi
    password=$(state_get '.panel.password'); password=${password:-Rm9$(random_hex 30)}
    gate=$(state_get '.panel.gate'); gate=${gate:-$(random_hex 32)}
    state_edit '.panel = ((.panel // {}) + {domain:$name,sub_domain:$sub,email:$email,ip:$ip,admin:$admin,password:$password,gate:$gate})' --arg name "$name" --arg sub "$subdomain" --arg email "$email" --arg ip "$ip" --arg admin "$admin" --arg password "$password" --arg gate "$gate"
    packages
    mkdir -p "$PANEL"
    if [[ ! -f $PANEL/.env ]]; then
        download https://api.github.com/repos/remnawave/backend/releases/latest "$ROOT/backend-release.json"
        version=$(json_get "$ROOT/backend-release.json" .tag_name)
        [[ $version =~ ^v?[0-9][a-zA-Z0-9._-]*$ ]] || die 'Неверный tag backend.'
        staging=$ROOT/panel-bootstrap
        download "https://raw.githubusercontent.com/remnawave/backend/$version/.env.sample" "$staging/.env"
        download "https://raw.githubusercontent.com/remnawave/backend/$version/docker-compose-prod.yml" "$staging/docker-compose.yml"
        dbpass=$(random_hex 32)
        env_set "$staging/.env" APP_SECRET "$(random_hex 64)"
        env_set "$staging/.env" METRICS_PASS "$(random_hex 32)"
        env_set "$staging/.env" WEBHOOK_SECRET_HEADER "$(random_hex 32)"
        env_set "$staging/.env" POSTGRES_PASSWORD "$dbpass"
        env_set "$staging/.env" DATABASE_URL "\"postgresql://postgres:$dbpass@remnawave-db:5432/postgres\""
        env_set "$staging/.env" PANEL_DOMAIN "$name"
        env_set "$staging/.env" FRONT_END_DOMAIN "$name"
        env_set "$staging/.env" SUB_PUBLIC_DOMAIN "$subdomain"
        sed -E "s|image: (ghcr.io/)?remnawave/backend:[^[:space:]]+|image: remnawave/backend:${version#v}|" "$staging/docker-compose.yml" | save "$PANEL/docker-compose.yml"
        save "$PANEL/.env" <"$staging/.env"
        state_edit '.panel.version = $version' --arg version "$version"
    fi
    compose "$PANEL" config --quiet
    compose "$PANEL" pull
    compose "$PANEL" up -d --wait --wait-timeout 240
    # Production Remnawave closes the socket without trusted HTTPS proxy headers,
    # even though its separate metrics healthcheck already reports healthy.
    wait_http http://127.0.0.1:3000/api/auth/status -H 'X-Forwarded-Proto: https' -H 'X-Forwarded-For: 127.0.0.1'
    if [[ -z $(state_get '.token') || -z $(state_get '.subscription_token') ]]; then
        response=$(API_ANONYMOUS=1 api GET /auth/status)
        auth=login
        if [[ $(jq -r .isRegisterAllowed <<<"$response") == true ]]; then auth=register; fi
        response=$(API_ANONYMOUS=1 api POST "/auth/$auth" "$(jq -n --arg username "$admin" --arg password "$password" '{username:$username,password:$password}')")
        token=$(jq -er .accessToken <<<"$response")
        if [[ -z $(state_get '.token') ]]; then
            response=$(API_TOKEN=$token API_ADMIN=1 api POST /tokens '{"name":"RemnaManager","expiresInDays":3650,"scopes":["*"]}')
            manager_token=$(jq -er '.token | select(type == "string" and length > 0)' <<<"$response")
            state_edit '.token = $token' --arg token "$manager_token"
        fi
        if [[ -z $(state_get '.subscription_token') ]]; then create_subscription_token "$token"; fi
    fi
    site "$name" "$email" 3000 "$gate"
    state_edit '.panel.ready = true'
    credentials
    if confirm 'Сразу установить страницу подписки?'; then sub_install preset; fi
}
panel_update() {
    local version path=$PANEL/docker-compose.yml old count
    backup
    download https://api.github.com/repos/remnawave/backend/releases/latest "$ROOT/backend-release.json"
    version=$(json_get "$ROOT/backend-release.json" .tag_name)
    [[ $version =~ ^v?[0-9][a-zA-Z0-9._-]*$ ]] || die 'Неверный tag backend.'
    printf 'Целевая версия: %s. Changelog: https://github.com/remnawave/backend/releases/tag/%s\n' "$version" "$version"
    if ! confirm 'Применить обновление? Миграции БД могут требовать восстановления из копии при откате.'; then return; fi
    count=$(grep -Ec 'image: (ghcr.io/)?remnawave/backend:[^[:space:]]+' "$path")
    [[ $count == 1 ]] || die 'Не удалось однозначно определить образ backend.'
    old=$(mktemp "$ROOT/panel-compose.XXXXXXXX"); cp "$path" "$old"
    sed -E "s|image: (ghcr.io/)?remnawave/backend:[^[:space:]]+|image: remnawave/backend:${version#v}|" "$old" | save "$path"
    if compose "$PANEL" config --quiet && compose "$PANEL" pull remnawave && compose "$PANEL" up -d --wait --wait-timeout 240; then
        rm -f "$old"; state_edit '.panel.version = $version' --arg version "$version"; return
    fi
    mv -f "$old" "$path"
    die 'Compose восстановлен. Автоматического отката миграций БД нет; копия сохранена.'
}
token_change() {
    local token
    token=$(ask 'API-токен панели' '' secret)
    API_TOKEN=$token api GET /nodes >/dev/null
    state_edit '.token = $token' --arg token "$token"
}
sub_install() {
    local token response password subdomain email
    [[ -n $(state_get '.panel.domain') ]] || die 'Сначала установите или подключите панель.'
    subdomain=$(state_get '.panel.sub_domain')
    email=$(state_get '.panel.email')
    if [[ ${1:-ask} != preset ]]; then
        subdomain=$(ask 'Домен страницы подписки' "$subdomain")
        email=$(ask 'Email для сертификата' "$email")
    fi
    subdomain=$(domain "$subdomain")
    [[ $subdomain != "$(state_get '.panel.domain')" ]] || die 'Панели и подписке нужны разные домены.'
    [[ $email =~ ^[^[:space:]@]+@[^[:space:]@]+\.[^[:space:]@]+$ ]] || die 'Неверный email ACME.'
    state_edit '.panel.sub_domain = $domain | .panel.email = $email | .subscription = false' --arg domain "$subdomain" --arg email "$email"
    # Provision the public endpoint before waiting on the subscription process.
    # A startup failure must not silently skip its domain and certificate.
    site "$subdomain" "$email" 3010
    if [[ -z $(state_get '.subscription_token') ]]; then
        password=$(ask 'Пароль администратора для отдельного токена подписки' '' secret)
        response=$(API_ANONYMOUS=1 api POST /auth/login "$(jq -n --arg username "$(state_get '.panel.admin')" --arg password "$password" '{username:$username,password:$password}')")
        create_subscription_token "$(jq -er .accessToken <<<"$response")"
    fi
    token=$(state_get '.subscription_token')
    mkdir -p "$SUB"
    if [[ ! -f $SUB/compose.yaml ]]; then subscription_compose | save "$SUB/compose.yaml"; fi
    env_set "$SUB/.env" APP_PORT 3010
    env_set "$SUB/.env" REMNAWAVE_PANEL_URL http://remnawave:3000
    env_set "$SUB/.env" REMNAWAVE_API_TOKEN "$token"
    env_set "$SUB/.env" TRUST_PROXY 1
    compose "$SUB" config --quiet
    sub_update
    env_set "$PANEL/.env" SUB_PUBLIC_DOMAIN "$(state_get '.panel.sub_domain')"
    compose "$PANEL" up -d --wait --wait-timeout 180
    state_edit '.subscription = true'
    printf 'Страница подписки: https://%s/<shortUuid>\n' "$(state_get '.panel.sub_domain')"
}
sub_update() { compose "$SUB" pull; compose "$SUB" up -d --wait --wait-timeout 120; wait_http http://127.0.0.1:3010/ -H 'X-Forwarded-Proto: https' -H 'X-Forwarded-For: 127.0.0.1'; }
sub_custom() {
    local image old
    menu 'Кастомизация подписки' '1. Официальный шаблон' '2. Свой совместимый Docker-образ' '3. Subpage Builder' '0. Назад'
    case $CHOICE in
        1) image=remnawave/subscription-page:latest;;
        2)
            image=$(ask 'Docker-образ (API подписки, порт 3010)')
            [[ $image =~ ^[a-zA-Z0-9][a-zA-Z0-9._/:@-]{0,255}$ ]] || die 'Неверное имя образа.'
            if ! confirm "Запустить сторонний образ $image? Он получит токен подписки."; then return; fi;;
        3) printf 'В панели: Subpage Builder — логотип, тексты, приложения и инструкции.\nhttps://docs.rw/install/subscription-page/bundled/#configuring-subscription-page-optional\n'; return;;
        *) return;;
    esac
    old=$(mktemp "$ROOT/sub-compose.XXXXXXXX"); cp "$SUB/compose.yaml" "$old"
    subscription_compose "$image" | save "$SUB/compose.yaml"
    # Keep readiness in a separate Bash process so errexit remains active inside it.
    if bash "$APP_DIR/manager.sh" --operation sub_update; then rm -f "$old"; return; fi
    mv -f "$old" "$SUB/compose.yaml"
    compose "$SUB" up -d
    die 'Новый образ не запустился; прежняя конфигурация восстановлена.'
}
