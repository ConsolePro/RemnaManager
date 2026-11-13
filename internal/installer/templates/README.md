# Шаблоны установки

`panel.compose.yml` и `panel.env` скачаны из официального backend 13.11.2026:

- https://raw.githubusercontent.com/remnawave/backend/refs/heads/main/docker-compose-prod.yml
- https://raw.githubusercontent.com/remnawave/backend/refs/heads/main/.env.sample

Compose Nginx, Caddy и подписки соответствуют примерам docs.rw. В Caddy
изменены только volumes: постоянные данные сохраняются в локальных каталогах.
При импорте сертификата добавляется bind-mount каталога nginx с PEM-файлами.

Файлы `*-cookie-*`, `*-tinyauth-*`, `*-mfa-*` — готовые варианты прокси с защитой
из исходного задания. Во время установки заменяются только обозначенные
значения доменов, email и секретов. Вариант separate не содержит upstream
локальной подписки. `*-subscription.conf` обслуживают только удалённую подписку.

Основные имена и сеть сохраняются как в документации. Образ приложения можно
изменить точечной правкой `image:`; остальные строки YAML сохраняются.
