package ui

type item struct {
	ru, en, action string
	children       []item
}

func leaf(ru, en string) item                    { return item{ru: ru, en: en, action: "pending"} }
func operation(ru, en, action string) item       { return item{ru: ru, en: en, action: action} }
func group(ru, en string, children ...item) item { return item{ru: ru, en: en, children: children} }
func menu() []item {
	return []item{
		group("Панель и компоненты", "Panel and components",
			item{ru: "Установить панель", en: "Install panel", action: "install"},
			operation("Перезапустить панель и компоненты", "Restart panel and components", "panel-restart"),
			operation("Запустить панель и компоненты", "Start panel and components", "panel-start"),
			operation("Доступ к панели", "Panel access", "panel-credentials"),
			operation("Восстановить HTTPS в firewall", "Repair HTTPS firewall", "panel-firewall-repair"),
			operation("Защита панели", "Panel protection", "panel-protection"),
			operation("Сменить обратный прокси", "Change reverse proxy", "panel-proxy"),
			group("Резервные копии и откат", "Backups and restore", operation("Создать копию", "Create backup", "panel-backup"), operation("Список копий", "List backups", "panel-backup-list"), operation("Проверить копию", "Verify backup", "panel-backup-verify"), operation("Восстановить БД и конфигурацию", "Restore database and configuration", "panel-restore"), operation("Удалить выбранную копию", "Delete selected backup", "panel-backup-delete")),
			operation("Версия панели и обновления", "Panel version and releases", "panel-version"),
			operation("Диагностика прокси и WASM", "Proxy and WASM diagnostics", "panel-diagnostics"),
			operation("Удалить панель и данные", "Remove panel and data", "panel-remove"),
			operation("Обновить панель", "Update panel", "panel-update"), operation("Остановить панель", "Stop panel", "panel-stop"), operation("Логи панели", "Panel logs", "panel-logs"),
			group("Remnawave CLI", "Remnawave CLI", operation("Статус компонентов", "Component status", "panel-status"), operation("Доступ к панели", "Panel credentials", "panel-credentials"), operation("Резервная копия БД", "Database backup", "panel-backup"), operation("Сменить API-токен", "Change API token", "panel-token"), operation("Запустить панель", "Start panel", "panel-start"), operation("CLI контейнера", "Container CLI", "panel-console")),
			group("Страница подписки", "Subscription page", operation("Установка / запуск", "Install / start", "sub-start"), operation("Перезапустить", "Restart", "sub-restart"), operation("Проверка состояния", "Check status", "sub-status"), operation("Обновление", "Update", "sub-update"), operation("Кастомизация", "Customize", "sub-custom"), operation("Остановить", "Stop", "sub-stop"), group("SSH-доступ", "SSH access", operation("Параметры и последняя ошибка", "Settings and last error", "sub-ssh-details"), operation("Проверить подключение", "Check connection", "sub-check"), operation("Изменить SSH / заменить ключ", "Edit SSH / replace key", "sub-ssh-edit")), operation("Удалить удалённую подписку", "Remove remote subscription", "sub-remove"))),
		operation("Ноды и расширения", "Nodes and extensions", "nodes-list"),
		group("Маршрутизация Xray и WARP", "Xray routing and WARP", operation("Настроить маршрутизацию: РФ напрямую", "Set up routing: Russia direct", "route-create"), operation("Установить WARP Native на ноду", "Install WARP Native on a node", "node-warp-install"), operation("Удалить WARP Native с ноды", "Remove WARP Native from a node", "node-warp-remove"), operation("Ранее созданные схемы", "Previously created schemes", "route-list")),
		group("Мониторинг", "Monitoring", leaf("Установить Xray Checker", "Install Xray Checker"), leaf("Статус и логи", "Status and logs"), leaf("Перезапустить", "Restart"), leaf("Обновить образы", "Update images"), leaf("Настроить Telegram", "Configure Telegram"), leaf("Удалить", "Remove")),
		group("Плагины", "Plugins", leaf("Включить BBR + fq", "Enable BBR + fq"), leaf("Диагностика и логи", "Diagnostics and logs")),
		group("Версии и обновления", "Versions and updates", item{ru: "Проверить обновления", en: "Check for updates", action: "check"}, operation("Обновить менеджер", "Update manager", "manager-update"), operation("Выбрать версию", "Select version", "manager-select"), operation("Откатить версию", "Roll back", "manager-rollback")),
		group("Настройки", "Settings", item{ru: "Язык и email администратора", en: "Language and administrator email", action: "settings"}, operation("Удалить менеджер с сервера", "Remove manager from server", "manager-remove")),
	}
}
