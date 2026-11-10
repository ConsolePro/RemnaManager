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
	case "panel-status", "panel-logs", "panel-credentials":
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
		m.problem = msg.err.Error()
	} else {
		m.problem = ""
	}
	return m, nil
}

func (m Model) updatePanelScreen(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	switch m.screen {
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
				m.problem = err.Error()
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
		if m.panelRequest.Action == "panel-token" {
			label = m.l("Новый токен управления (ввод скрыт):", "New manager token (hidden input):")
		}
		if m.panelRequest.Action == "sub-image" {
			label = m.l("Docker-образ (API, порт 3010, HEALTHCHECK):", "Docker image (API, port 3010, HEALTHCHECK):")
		}
		text := title + "\n\n" + label + "\n" + m.inputs[0].View()
		if m.panelRequest.Action == "panel-update" {
			text += "\n\nhttps://github.com/remnawave/backend/releases"
		}
		return text + "\n\n" + m.l("Enter — проверить и продолжить · Esc — назад", "Enter — validate and continue · Esc — back")
	case "panelConfirm":
		text := title + "\n\n"
		if m.panelRequest.Value != "" && m.panelRequest.Action != "panel-token" {
			text += m.panelRequest.Value + "\n\n"
		}
		description := m.l("Операция изменит компоненты управляемого стека.", "This operation changes components in the managed stack.")
		switch m.panelRequest.Action {
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
