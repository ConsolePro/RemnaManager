package ui

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/ConsolePro/RemnaManager/internal/installer"
	"github.com/ConsolePro/RemnaManager/internal/services"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
)

type panelResultMsg struct {
	run    int
	result services.PanelResult
	err    error
}

func (m Model) openPanelOperation(selected item) (tea.Model, tea.Cmd) {
	if _, ok := m.service.(services.PanelManager); !ok {
		m.screen = "notice"
		m.notice = m.l("Деморежим: управление настоящими компонентами отключено. Запустите приложение на сервере без --demo.", "Demo mode: real component management is disabled. Run on your server without --demo.")
		return m, nil
	}
	m.cancelInstall()
	m.run++
	m.problem = ""
	m.scroll = 0
	m.panelResult = ""
	m.panelRequest = services.PanelRequest{Action: selected.action}
	m.panelTitle = m.l(selected.ru, selected.en)
	switch selected.action {
	case "panel-domains":
		p := services.PanelParameters{}
		if live, ok := m.liveInstaller(); ok {
			p = live.Defaults()
		}
		m.inputs = []textinput.Model{newInput("panel.example.com", p.PanelDomain+" "+p.SubscriptionDomain)}
		m.focus = 0
		m.screen = "panelInput"
		return m, m.inputs[0].Focus()
	case "panel-certificates":
		m.prepareInstallInputs()
		m.editingCertificates = true
		if m.proxyChoice == 1 {
			m.notice = m.l("Caddy автоматически продлевает сертификаты. Для проверки HTTPS используйте диагностику прокси.", "Caddy renews certificates automatically. Use proxy diagnostics to check HTTPS.")
			m.screen = "notice"
			return m, nil
		}
		m.screen = "certificateOptions"
		return m, nil

	case "sub-ssh-edit":
		m.remoteInstallWizard = false
		if live, ok := m.liveInstaller(); ok && live.Defaults().RemoteSubscription != nil {
			m.remoteDraft = *live.Defaults().RemoteSubscription
		}
		return m.openRemoteInputs()
	case "panel-proxy", "panel-backup-verify", "panel-backup-delete", "panel-restore", "panel-diagnostics":
		placeholder := "backup-YYYYMMDD-HHMMSS-ID"
		if selected.action == "panel-proxy" {
			placeholder = "nginx / caddy"
		}
		if selected.action == "panel-diagnostics" {
			placeholder = "/assets/example.wasm (optional)"
		}
		m.inputs = []textinput.Model{newInput(placeholder, "")}
		m.focus = 0
		m.screen = "panelInput"
		return m, m.inputs[0].Focus()
	case "panel-remove", "sub-remove":
		m.screen = "panelInput"
		m.inputs = []textinput.Model{newInput("DELETE", "")}
		m.focus = 0
		return m, m.inputs[0].Focus()
	case "panel-protection":
		m.screen = "panelProtection"
		m.protectionChoice = 0
	case "sub-custom":
		m.screen = "panelCustom"
		m.customChoice = 0
	case "panel-update", "panel-token":
		m.screen = "panelInput"
		placeholder := "3.4.9"
		if selected.action == "panel-token" {
			placeholder = "API token"
		}
		input := newInput(placeholder, "")
		input.Width = max(1, min(60, m.width-12))
		if selected.action == "panel-token" {
			input.EchoMode = textinput.EchoPassword
			input.CharLimit = 4096
		}
		m.inputs = []textinput.Model{input}
		m.focus = 0
		return m, m.inputs[0].Focus()
	case "panel-status", "panel-logs", "panel-credentials", "panel-inspect", "panel-backup-list", "panel-version", "sub-status":
		return m.startPanelOperation()
	default:
		m.screen = "panelConfirm"
	}
	return m, nil
}

func (m Model) startPanelOperation() (tea.Model, tea.Cmd) {
	service, ok := m.service.(services.PanelManager)
	if !ok {
		return m, nil
	}
	m.cancelInstall()
	m.installCtx, m.installCancel = context.WithCancel(m.ctx)
	m.screen = "panelProgress"
	m.problem = ""
	m.scroll = 0
	run, request, ctx := m.run, m.panelRequest, m.installCtx
	return m, func() tea.Msg {
		operation, cancel := context.WithTimeout(ctx, 30*time.Minute)
		defer cancel()
		result, err := service.Manage(operation, request)
		return panelResultMsg{run, result, err}
	}
}
func (m Model) receivePanelResult(msg panelResultMsg) (tea.Model, tea.Cmd) {
	if msg.run != m.run || m.screen != "panelProgress" {
		return m, nil
	}
	m.cancelInstall()
	if m.panelRequest.Action == "sub-ssh-scan" && msg.err != nil && m.remoteInstallWizard {
		m.screen = "panelRemoteInput"
		m.problem = m.errorText(msg.err)
		return m, nil
	}
	if m.panelRequest.Action == "sub-ssh-scan" && msg.err == nil {
		m.remoteDraft = msg.result.Remote
		m.remoteFingerprint = msg.result.Fingerprint
		m.screen = "panelRemoteTrust"
		return m, nil
	}
	if m.panelRequest.Action == "panel-inspect" && msg.err == nil {
		items := menu()[0].children
		if msg.result.Installation == "absent" {
			for _, entry := range items {
				if entry.en == "Backups and restore" {
					items = []item{items[0], entry}
					break
				}
			}
		} else if msg.result.Installation != "partial" {
			items = items[1:]
		}
		m.frames = append(m.frames, frame{items: items, title: "Управление панелью", titleEN: "Panel management"})
		if msg.result.Remote.SSH.Host != "" {
			last := len(m.frames) - 1
			m.frames[last].items = append(m.frames[last].items, group("Сохранённая удалённая подписка", "Preserved remote subscription", operation("Статус", "Status", "sub-status"), operation("SSH-доступ", "SSH access", "sub-ssh-details"), operation("Удалить удалённую подписку", "Remove remote subscription", "sub-remove")))
		}
		m.screen = "panelResult"
		m.panelResult = msg.result.Text
		if m.cfg.Language != "en" && msg.result.TextRU != "" {
			m.panelResult = msg.result.TextRU
		}
		return m, nil
	}
	if len(msg.result.Command) > 0 && msg.err == nil {
		command := msg.result.Command
		cmd := exec.CommandContext(m.ctx, command[0], command[1:]...)
		run := m.run
		return m, tea.ExecProcess(cmd, func(err error) tea.Msg {
			return panelResultMsg{run, services.PanelResult{Text: m.l("CLI контейнера завершён.", "Container CLI exited.")}, err}
		})
	}
	m.panelRequest.Value = ""
	if len(m.inputs) > 0 && m.inputs[0].EchoMode == textinput.EchoPassword {
		m.inputs[0].SetValue("")
	}
	m.screen = "panelResult"
	m.scroll = 0
	m.panelResult = msg.result.Text
	if m.cfg.Language != "en" && msg.result.TextRU != "" {
		m.panelResult = msg.result.TextRU
	}
	if msg.err != nil {
		m.problem = m.errorText(msg.err)
	} else {
		m.problem = ""
	}
	return m, nil
}

func (m Model) updatePanelScreen(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	switch m.screen {
	case "panelRemoteInput", "panelRemoteTrust":
		return m.updateRemoteInput(msg)
	case "panelProtection":
		switch key {
		case "up", "down", "left", "right", "tab", "shift+tab":
			m.moveProtectionChoice(key)
		case "enter":
			m.panelRequest.Protection = protectionChoices[m.protectionChoice]
			if m.protectionChoice != 1 {
				m.screen = "panelConfirm"
			} else {
				m.inputs = []textinput.Model{newInput("auth.panel.example.com", "")}
				m.focus = 0
				m.screen = "panelInput"
				return m, m.inputs[0].Focus()
			}
		}
	case "panelCustom":
		switch key {
		case "up", "left", "shift+tab":
			m.customChoice = (m.customChoice + 2) % 3
		case "down", "right", "tab":
			m.customChoice = (m.customChoice + 1) % 3
		case "enter":
			if m.customChoice == 2 {
				m.screen = "notice"
				m.notice = m.l("В панели откройте Subpage Builder: логотип, тексты, приложения и инструкции.\n", "Open Subpage Builder in your panel for logos, text, apps and instructions.\n") + "https://docs.rw/install/subscription-page/bundled/#configuring-subscription-page-optional"
				return m, nil
			}
			m.panelRequest.Action = "sub-image"
			if m.customChoice == 0 {
				m.panelRequest.Value = "remnawave/subscription-page:latest"
				m.screen = "panelConfirm"
			} else {
				m.inputs = []textinput.Model{newInput("ghcr.io/owner/subscription:tag", "")}
				m.focus = 0
				m.screen = "panelInput"
				return m, m.inputs[0].Focus()
			}
		}
	case "panelInput":
		if key == "enter" {
			value := strings.TrimSpace(m.inputs[0].Value())
			var err error
			switch m.panelRequest.Action {
			case "panel-domains":
				names := strings.Fields(strings.ToLower(value))
				if len(names) != 2 || !installer.ValidDomain(names[0]) || !installer.ValidDomain(names[1]) || names[0] == names[1] {
					err = fmt.Errorf("enter two different domains separated by a space")
				} else {
					value = strings.Join(names, " ")
				}
			case "panel-proxy":
				value = strings.ToLower(value)
				if value != "nginx" && value != "caddy" {
					err = fmt.Errorf("choose nginx or caddy")
				}
			case "panel-backup-verify", "panel-backup-delete", "panel-restore":
				if !strings.HasPrefix(value, "backup-") || strings.ContainsAny(value, "/\\:") {
					err = fmt.Errorf("enter a backup name from the list")
				}
			case "panel-remove", "sub-remove":
				if m.panelRequest.Action == "panel-remove" && value == "DELETE ALL" {
					m.panelRequest.DeleteBackups = true
					value = "DELETE"
				}
				if value != "DELETE" {
					err = fmt.Errorf("%s", m.l("Для удаления введите DELETE", "Type DELETE to confirm removal"))
				}
			case "panel-protection":
				value = strings.ToLower(value)
				if !installer.ValidDomain(value) {
					err = fmt.Errorf("enter a TinyAuth domain such as auth.panel.example.com")
				}
			case "panel-update":
				err = installer.ValidateBackendVersion(value)
			case "sub-image":
				err = installer.ValidateSubscriptionImage(value)
			case "panel-token":
				if len(value) < 16 || len(value) > 4096 || strings.ContainsAny(value, " \r\n\t") {
					err = fmt.Errorf("invalid API token")
				}
			}
			if err != nil {
				m.problem = m.errorText(err)
				return m, nil
			}
			m.problem = ""
			m.panelRequest.Value = value
			m.screen = "panelConfirm"
			m.inputs[0].Blur()
			return m, nil
		}
		var cmd tea.Cmd
		m.inputs[0], cmd = m.inputs[0].Update(msg)
		return m, cmd
	case "panelConfirm":
		if key == "enter" {
			return m.startPanelOperation()
		}
	case "panelResult":
		if key == "enter" {
			m.screen = "menu"
			m.problem = ""
			m.panelResult = ""
		}
	}
	return m, nil
}

func (m Model) panelScreenView() string {
	title := accent.Render(m.panelTitle)
	switch m.screen {
	case "panelProtection":
		return m.protectionOptionsView()
	case "panelCustom":
		choices := [][2]string{{"Официальный образ", "Official image"}, {"Свой совместимый Docker-образ", "Custom compatible Docker image"}, {"Subpage Builder в панели", "Subpage Builder in panel"}}
		rows := []string{title, ""}
		for i, choice := range choices {
			s := "  " + m.l(choice[0], choice[1])
			if i == m.customChoice {
				s = accent.Render("› " + m.l(choice[0], choice[1]))
			}
			rows = append(rows, s)
		}
		return strings.Join(rows, "\n")
	case "panelInput":
		label := m.l("Версия backend (стабильный тег):", "Backend version (stable tag):")
		switch m.panelRequest.Action {
		case "panel-proxy":
			label = m.l("Новый прокси: nginx или caddy. Текущая защита сохраняется.", "New proxy: nginx or caddy. Current protection is preserved.")
		case "panel-backup-verify", "panel-backup-delete", "panel-restore":
			label = m.l("Имя каталога из списка резервных копий:", "Directory name from the backup list:")
		case "panel-diagnostics":
			label = m.l("Точный путь WASM из Network браузера; пустое поле — диагностика прокси:", "Exact WASM path from browser Network; leave empty for proxy diagnostics:")
		}
		if m.panelRequest.Action == "panel-remove" || m.panelRequest.Action == "sub-remove" {
			label = m.l("Будут безвозвратно удалены панель, подписка, БД и данные TinyAuth/MFA.\nРезервную копию можно сделать в Remnawave CLI → Резервная копия БД.\nДля продолжения введите DELETE:", "Panel, subscription, database and TinyAuth/MFA data will be permanently deleted.\nYou can create a backup in Remnawave CLI → Database backup.\nType DELETE to continue:")
		}
		if m.panelRequest.Action == "panel-remove" {
			label += "\n" + m.l("DELETE — сохранить копии; DELETE ALL — удалить также резервные копии.", "DELETE preserves backups; DELETE ALL also removes backups.")
		}
		if m.panelRequest.Action == "sub-remove" {
			label = m.l("Удалить только удалённую подписку и её прокси? Панель останется. Введите DELETE:", "Remove only the remote subscription and its proxy? The panel remains. Type DELETE:")
		}
		if m.panelRequest.Action == "panel-token" {
			label = m.l("Новый токен управления (ввод скрыт):", "New manager token (hidden input):")
		}
		if m.panelRequest.Action == "sub-image" {
			label = m.l("Docker-образ (API, порт 3010, HEALTHCHECK):", "Docker image (API, port 3010, HEALTHCHECK):")
		}
		if m.panelRequest.Action == "panel-protection" {
			label = m.l("Домен TinyAuth (прямой поддомен панели, A/AAAA на сервер):", "TinyAuth domain (direct child of panel domain, DNS pointing here):")
		}
		if m.panelRequest.Action == "panel-domains" {
			label = m.l("Домен панели и домен подписки через пробел:", "Panel domain and subscription domain separated by a space:")
		}
		text := title + "\n\n" + label + "\n" + m.inputs[0].View()
		if m.panelRequest.Action == "panel-update" {
			text += "\n\nhttps://github.com/remnawave/backend/releases"
		}
		return text + "\n\n" + m.l("Enter — проверить и продолжить · Esc — назад", "Enter — validate and continue · Esc — back")
	case "panelConfirm":
		text := title + "\n\n"
		if live, ok := m.liveInstaller(); ok && (m.panelRequest.Action == "panel-proxy" || m.panelRequest.Action == "panel-protection") {
			p := live.Defaults()
			text += fmt.Sprintf("%s: %s\n%s: %s\n", m.l("Текущий прокси", "Current proxy"), p.Proxy, m.l("Текущая защита", "Current protection"), p.Protection)
		}
		if m.panelRequest.Value != "" && m.panelRequest.Action != "panel-token" {
			text += m.panelRequest.Value + "\n\n"
		}
		description := m.l("Операция изменит компоненты управляемого стека.", "This operation changes components in the managed stack.")
		switch m.panelRequest.Action {
		case "panel-domains":
			description = m.l("Будут проверены DNS и сертификаты, создана резервная копия и обновлены адреса панели и подписки. При ошибке будет выполнен откат. Размещение серверов сохраняется.", "DNS and certificates will be checked, a backup created and panel/subscription domains updated. A failure triggers rollback. Server placement is preserved.")
		case "panel-certificates":
			description = m.l("Сохранить выбранный способ SSL, проверить сертификаты и обновить HTTPS и продление?", "Save the selected SSL method, verify certificates and update HTTPS and renewal?")
		case "panel-firewall-repair":
			description = m.l("Обновить firewall панели, чтобы HTTP/HTTPS проходили в Docker?\nСохраняются разрешённые порты SSH. Правила будут действовать после перезагрузки.", "Update the panel firewall to allow published HTTP/HTTPS into Docker?\nThe SSH allowlist is preserved. Rules persist across reboots.")
		case "panel-proxy":
			description = m.l("Будет создана резервная копия и проверена новая конфигурация.\nПри сбое HTTPS будет запущен прежний прокси.\nВозможен короткий простой. Существующие сертификаты сохраняются.\nПереход с системной службы на Docker выполняется только этой операцией.", "A backup and configuration validation precede switching.\nHTTPS failure restarts the previous proxy. Brief downtime is possible.\nCertificates are preserved. This explicitly migrates legacy system services to Docker.")
			description += "\n" + m.l("В новом формате Nginx требует общий SAN-сертификат. Если его нет, будет запрошен новый общий сертификат через ACME.", "The new Nginx layout requires a shared SAN certificate. If absent, a new shared certificate will be requested through ACME.")
		case "panel-restore":
			description = m.l("ЗАМЕНИТЬ PostgreSQL и конфигурацию выбранной копией?\nТекущие изменения данных будут потеряны. Будет создана копия pre-restore.\nВосстанавливаются согласованные версия приложения и база; требуется совпадение доменов, защиты и прокси.", "REPLACE PostgreSQL and configuration with this snapshot?\nCurrent data changes will be lost. A pre-restore backup is created first.\nThe matching application version and database are restored; domains, protection and proxy must match.")
		case "panel-backup-delete":
			description = m.l("Безвозвратно удалить только выбранную резервную копию?", "Permanently delete only this selected backup?")
		case "panel-diagnostics":
			description = m.l("Прочитать конфигурацию и логи прокси, проверить WASM двумя запросами.\nДля TinyAuth/MFA без браузерной сессии возможен ответ авторизации.\nДиагностика не изменяет настройки.", "Read proxy configuration and logs; request WASM twice.\nTinyAuth/MFA may return an authorization response without a browser session.\nDiagnostics do not change settings.")
		case "panel-remove":
			description = m.l("Безвозвратно удалить панель, подписку, БД и данные TinyAuth/MFA?\nБудут удалены настройки прокси и firewall этой панели.\nУдаление правил firewall может открыть другие порты сервера.\nРезервные копии, сертификаты, пакеты, менеджер и удалённые ноды сохранятся.\nПосле удаления можно заново установить панель.", "Permanently remove panel, subscription, database and TinyAuth/MFA data?\nThis panel's proxy configuration and firewall rules will be removed.\nRemoving firewall rules may expose other server ports.\nBackups, certificates, packages, manager and remote nodes will be preserved.\nYou can install the panel again afterwards.")
			if m.panelRequest.DeleteBackups {
				description = m.l("Безвозвратно удалить панель, данные и ВСЕ резервные копии этой установки?\nСертификаты и удалённые серверы сохраняются.", "Permanently remove the panel, data and ALL backups of this installation?\nCertificates and remote servers are preserved.")
			}
		case "sub-remove":
			description = m.l("Безвозвратно удалить конфигурацию подписки на удалённом сервере? Сертификаты и резервные файлы сохраняются.", "Permanently remove the subscription configuration on the remote server? Certificates and recovery files are preserved.")
		case "panel-protection":
			description = m.l("Будет включена защита: ", "Enable protection: ") + m.panelRequest.Protection + "\n" + m.l("Сервисы панели будут запущены, прокси перенастроен.\nДанные дополнительного входа будут сохранены и показаны.\nПароль панели не меняется.\nПри повторе после ошибки выберите тот же режим и домен.", "Panel services will be started and the proxy reconfigured.\nThe additional login credentials will be saved and shown.\nThe panel password is unchanged.\nAfter an error retry the same mode and domain.")
			if m.panelRequest.Protection == "mfa" {
				description += "\n" + m.l("При первом входе в /r обязательна настройка второго фактора.", "First login at /r requires enrolling a second factor.")
			}
		case "panel-update":
			description = m.l("Перед обновлением будет сохранена копия PostgreSQL и конфигурации.\nВозможны миграции БД и краткая недоступность панели.\nОткат миграций автоматически не выполняется.", "A PostgreSQL and configuration backup will be created first.\nDatabase migrations and brief downtime are possible.\nDatabase migrations are not rolled back automatically.")
			description += "\n\nhttps://github.com/remnawave/backend/releases/tag/" + m.panelRequest.Value
		case "panel-stop":
			description = m.l("Будут остановлены панель, подписка, PostgreSQL и Valkey.\nДанные, сертификаты и firewall сохранятся.", "Panel, subscription, PostgreSQL and Valkey will stop.\nData, certificates and firewall are preserved.")
		case "panel-backup":
			description = m.l("Сохранить PostgreSQL и конфигурацию в приватный каталог?\nПотребуется свободное место на диске сервера.", "Save PostgreSQL and configuration to a private directory?\nFree disk space is required on the server.")
		case "panel-token":
			description = m.l("Токен будет проверен через API и сохранён вместо текущего.\nТокен страницы подписки не меняется. Старый токен не отзывается.", "The token will be validated through the API and saved.\nThe subscription token is unchanged. The old token is not revoked.")
		case "sub-image":
			description = m.l("Образ получит ограниченный API-токен подписок.\nИспользуйте только доверенный совместимый образ.\nПри ошибке запуска прежняя конфигурация будет восстановлена.", "The image will receive the limited subscription API token.\nUse only a trusted compatible image.\nA startup failure restores the previous configuration.")
		case "panel-console":
			description = m.l("Откроется интерактивный CLI контейнера.\nПосле выхода вы вернётесь в RemnaManager.", "The interactive container CLI will open.\nExit it to return to RemnaManager.")
		}
		return text + warning.Render(description) + "\n\n" + m.l("Enter — выполнить · Esc — отменить", "Enter — execute · Esc — cancel")
	case "panelProgress":
		return title + "\n\n" + accent.Render("●") + " " + m.l("Выполняется…", "Running…") + "\n\n" + m.l("Esc — отменить дальнейшее выполнение", "Esc — cancel further execution")
	case "panelResult":
		return title + "\n\n" + m.panelResult + "\n\n" + m.l("PgUp/PgDn — прокрутка · Enter — в меню", "PgUp/PgDn — scroll · Enter — menu")
	}
	return ""
}
