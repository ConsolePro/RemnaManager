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
			operation("Доступ к панели (логин и пароль)", "Panel credentials (login and password)", "panel-credentials"),
			operation("Защита панели", "Panel protection", "panel-protection"),
			operation("Удалить панель и данные", "Remove panel and data", "panel-remove"),
			operation("Обновить панель", "Update panel", "panel-update"), operation("Остановить панель", "Stop panel", "panel-stop"), operation("Логи панели", "Panel logs", "panel-logs"),
			group("Remnawave CLI", "Remnawave CLI", operation("Статус компонентов", "Component status", "panel-status"), operation("Доступ к панели", "Panel credentials", "panel-credentials"), operation("Резервная копия БД", "Database backup", "panel-backup"), operation("Сменить API-токен", "Change API token", "panel-token"), operation("Запустить панель", "Start panel", "panel-start"), operation("CLI контейнера", "Container CLI", "panel-console")),
			group("Страница подписки", "Subscription page", operation("Установка / запуск", "Install / start", "sub-start"), operation("Обновление", "Update", "sub-update"), operation("Кастомизация", "Customize", "sub-custom"), operation("Остановить", "Stop", "sub-stop"))),
		operation("Ноды и расширения", "Nodes and extensions", "nodes-list"),
		group("WARP-Native", "WARP-Native", leaf("Установить WARP", "Install WARP"), leaf("Удалить WARP", "Remove WARP"), leaf("Добавить маршруты", "Add routes"), leaf("Удалить маршруты", "Remove routes")),
		group("Мониторинг", "Monitoring", leaf("Установить Xray Checker", "Install Xray Checker"), leaf("Статус и логи", "Status and logs"), leaf("Перезапустить", "Restart"), leaf("Обновить образы", "Update images"), leaf("Настроить Telegram", "Configure Telegram"), leaf("Удалить", "Remove")),
		group("Плагины", "Plugins", leaf("Включить BBR + fq", "Enable BBR + fq"), leaf("Диагностика и логи", "Diagnostics and logs")),
		group("Балансировщики и мосты", "Balancers and bridges", leaf("Клиентский балансировщик", "Client balancer"), leaf("Мост РФ → Европа", "Russia → Europe bridge"), leaf("Список / удаление", "List / remove"), leaf("Незавершённые операции", "Pending operations")),
		group("Версии и обновления", "Versions and updates", item{ru: "Проверить обновления", en: "Check for updates", action: "check"}, operation("Обновить менеджер", "Update manager", "manager-update"), operation("Выбрать версию", "Select version", "manager-select"), operation("Откатить версию", "Roll back", "manager-rollback")),
		group("Настройки", "Settings", item{ru: "Язык и email администратора", en: "Language and administrator email", action: "settings"}, operation("Удалить менеджер с сервера", "Remove manager from server", "manager-remove")),
	}
}
