# Исходная Bash-версия

Снимок исходного кода закреплён веткой `legacy/bash` до перехода на Go.
История, теги, Releases и исходные Bash-модули сохраняются. Рабочая ветка:
`feature/go-tui`. Не закоммиченные изменения пользователя в снимок не входят.

## Точки входа и распространение

`install.sh` загружал опубликованный установщик; `remnamanager.sh` и
`dist/remnamanager.sh` содержат упакованный дистрибутив. `build.sh` формирует
его по `app-files.txt`. `manager.sh` загружает модули, проверяет root/Linux,
открывает меню либо исполняет отдельную операцию. `agent.sh` принимает задания
для удалённой ноды. `lib/install.sh` и `lib/shortcuts.sh` обеспечивают команды
`remna_manager`, `re` и очистку устаревшего сокращения `rm`.

## Меню и функции

| Раздел | Реализованные операции | Модули |
|---|---|---|
| Панель и компоненты | Установка, обновление с pg_dump, остановка, логи; CLI: статус, доступ, резервная копия, смена API-токена, запуск, CLI контейнера | menu, panel, render |
| Ноды и расширения | Список API, добавление, незавершённые установки, selfsteal (3 шаблона/HTML), Xray с SHA256 и откатом, переименование, переустановка с прежними ключами, обновление, запуск/остановка, перезапуск, статус/логи, смена SSH | nodes, provision, ssh, render |
| Страница подписки | Установка/запуск, домен/email, HTTPS, обновление образа, кастомизация, остановка | panel, render |
| WARP-Native | Установка/удаление WireGuard/wgcf, регистрация Cloudflare после согласия, сохранение аккаунта, добавление/удаление доменных маршрутов Xray, nftables kill switch | warp, nodes |
| Мониторинг | Xray Checker: установка, статус/логи, перезапуск, обновление, Telegram, удаление; systemd-уведомления | monitor, render |
| Плагины | BBR + fq, диагностика и логи нод | nodes, provision |
| Версии и обновления | Каталог стабильных Releases, кэш 15 минут, выбор версии, обновление, сохранение старого кода, откат с сохранением данных | versions |
| Балансировщики и мосты | Клиентские Xray JSON пулы с observatory, согласование squads; мост РФ → Европа с geosite/geoip, служебным пользователем и проверкой Xray; удаление/отмена незавершённых операций | topology |

`lib/ui.sh` читает CPU/RAM/диск/интерфейсы, обновляет шапку каждые 2 секунды,
показывает IPv4/IPv6 и доступные обновления. `lib/runtime.sh` содержит валидацию,
атомарную запись JSON, API, Compose, загрузки, ожидание HTTP, подтверждения и
закрытые журналы. Ошибки дочерних операций возвращают пользователя в меню.

## Зависимости и операции с системой

Debian 12/13, Ubuntu 22.04/24.04; root, Bash 4.4+, systemd. Утилиты:
curl, jq, OpenSSL, OpenSSH, tar, gzip, unzip, iproute2, util-linux, gnupg,
Docker Engine/Compose, Nginx, Certbot, nftables, WireGuard, wgcf.
Проверки используют также shellcheck и контейнерный runtime.

Установка добавляет APT-репозиторий Docker, пакеты, Compose и env-файлы;
включает Docker/Nginx и Certbot timer; пишет Nginx, HTTP-01 webroot,
сертификаты и systemd units. Открытые контейнерные порты привязаны к loopback.
SSH root использует подтверждение SHA256 host key. Ноды получают ограничение
TCP 2222 IP-адресом панели. API создаёт профили Reality/Vision, хосты,
пользователей и internal squads. Секретный URL панели выдаёт защищённую cookie.

Состояние/секреты: `/opt/remnamanager/state.json`; SSH: `/opt/remnamanager/ssh`;
логи, backups, versions — под `/opt/remnamanager`. Панель: `/opt/remnawave`,
подписка — вложенный каталог; Checker — `/opt/remnamanager/checker`.
Каталоги секретов 0700, файлы 0600. Настройки не исполняются как shell-код.

В исходном коде URL распространения указывает на `OverlayNode/RemnaManager`;
новое ТЗ задаёт `ConsolePro/RemnaManager`. Remote автоматически не меняется.

## Проверки исходной реализации

`tests/run.sh`/`cases.sh`: validation, storage, configs, warp, SSH pinning,
Nginx rollback, API errors, readiness, resume, notifications, fail-fast,
dashboard, version catalog, confirmations, step output, native WARP,
bridge/balancer render, Reality key, distribution, bundle.
`container.sh`, `linux.sh`, `native-integration.sh`, `version-integration.sh`,
`live-fixture.sh`, `real-panel.sh` покрывают Linux и интеграцию с сервисами.
Опасные integration-сценарии нельзя запускать на действующем сервере.

Go MVP не вызывает эти Bash-операции. Реальные операции будут переноситься
за интерфейсами сервисов после проверки прототипа и отдельного согласования.
