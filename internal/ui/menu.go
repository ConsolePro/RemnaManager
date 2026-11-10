package ui

type item struct {
	ru, en, action string
	children       []item
}

func leaf(ru, en string) item                    { return item{ru: ru, en: en, action: "pending"} }
func group(ru, en string, children ...item) item { return item{ru: ru, en: en, children: children} }
func menu() []item {
	return []item{
		group("Панель и компоненты", "Panel and components",
			item{ru: "Установить панель", en: "Install panel", action: "install"},
			leaf("Обновить панель", "Update panel"), leaf("Остановить панель", "Stop panel"), leaf("Логи панели", "Panel logs"),
			group("Remnawave CLI", "Remnawave CLI", leaf("Статус компонентов", "Component status"), leaf("Доступ к панели", "Panel credentials"), leaf("Резервная копия БД", "Database backup"), leaf("Сменить API-токен", "Change API token"), leaf("Запустить панель", "Start panel"), leaf("CLI контейнера", "Container CLI")),
			group("Страница подписки", "Subscription page", leaf("Установка / запуск", "Install / start"), leaf("Обновление", "Update"), leaf("Кастомизация", "Customize"), leaf("Остановить", "Stop"))),
		group("Ноды и расширения", "Nodes and extensions", leaf("Список нод", "Node list"), leaf("Добавить ноду", "Add node"), leaf("Незавершённые установки", "Pending installations"),
			group("Управление нодой", "Manage node", leaf("Шаблон selfsteal", "Selfsteal template"), leaf("Обновить ядро Xray", "Update Xray"), leaf("Переименовать", "Rename"), leaf("Переустановить", "Reinstall"), leaf("Обновить ноду", "Update node"), leaf("Запустить / остановить", "Start / stop"), leaf("Перезапустить", "Restart"), leaf("Статус и логи", "Status and logs"), leaf("SSH-подключение", "SSH connection"))),
		group("WARP-Native", "WARP-Native", leaf("Установить WARP", "Install WARP"), leaf("Удалить WARP", "Remove WARP"), leaf("Добавить маршруты", "Add routes"), leaf("Удалить маршруты", "Remove routes")),
		group("Мониторинг", "Monitoring", leaf("Установить Xray Checker", "Install Xray Checker"), leaf("Статус и логи", "Status and logs"), leaf("Перезапустить", "Restart"), leaf("Обновить образы", "Update images"), leaf("Настроить Telegram", "Configure Telegram"), leaf("Удалить", "Remove")),
		group("Плагины", "Plugins", leaf("Включить BBR + fq", "Enable BBR + fq"), leaf("Диагностика и логи", "Diagnostics and logs")),
		group("Балансировщики и мосты", "Balancers and bridges", leaf("Клиентский балансировщик", "Client balancer"), leaf("Мост РФ → Европа", "Russia → Europe bridge"), leaf("Список / удаление", "List / remove"), leaf("Незавершённые операции", "Pending operations")),
		group("Версии и обновления", "Versions and updates", item{ru: "Проверить обновления", en: "Check for updates", action: "check"}, leaf("Обновить менеджер", "Update manager"), leaf("Выбрать версию", "Select version"), leaf("Откатить версию", "Roll back")),
		group("Настройки", "Settings", item{ru: "Язык и email администратора", en: "Language and administrator email", action: "settings"}),
	}
}
