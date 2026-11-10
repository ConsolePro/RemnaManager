package ui

import (
	"context"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/ConsolePro/RemnaManager/internal/installer"
	"github.com/ConsolePro/RemnaManager/internal/services"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
)

type nodeResultMsg struct {
	run    int
	action string
	result services.NodeResult
	err    error
}

func (m Model) openNodeOperation(selected item) (tea.Model, tea.Cmd) {
	if _, ok := m.service.(services.NodeManager); !ok {
		m.screen = "notice"
		m.notice = m.l("Деморежим: управление нодами отключено.", "Demo mode: node management is disabled.")
		return m, nil
	}
	if strings.HasPrefix(selected.action, "node-install-") {
		return m.openNodeInstall(selected)
	}
	m.nodeAction = selected.action
	m.nodeTitle = m.l(selected.ru, selected.en)
	m.nodeList = nil
	m.nodeCursor = 0
	return m.startNodeRequest(services.NodeRequest{Action: "nodes-list"})
}
func (m Model) startNodeRequest(request services.NodeRequest) (tea.Model, tea.Cmd) {
	service, ok := m.service.(services.NodeManager)
	if !ok {
		return m, nil
	}
	m.cancelInstall()
	m.run++
	m.installCtx, m.installCancel = context.WithCancel(m.ctx)
	m.nodeRequest = request
	m.screen = "nodesProgress"
	if request.Action == "node-install-step" {
		m.screen = "nodeInstallProgress"
	}
	m.problem = ""
	m.scroll = 0
	run, ctx := m.run, m.installCtx
	return m, func() tea.Msg {
		timeout := 45 * time.Second
		if request.Action == "node-install-step" {
			timeout = 20 * time.Minute
		}
		if request.Action == "node-install-confirm" {
			timeout = 90 * time.Second
		}
		operation, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		result, err := service.ManageNodes(operation, request)
		return nodeResultMsg{run, request.Action, result, err}
	}
}
func (m Model) receiveNodeResult(msg nodeResultMsg) (tea.Model, tea.Cmd) {
	if msg.run != m.run || (m.screen != "nodesProgress" && m.screen != "nodeInstallProgress") {
		return m, nil
	}
	m.cancelInstall()
	if msg.result.Install != nil {
		m.nodeInstallJob = msg.result.Install
		m.nodeInstallParams = msg.result.Install.Params
	}
	if msg.err != nil {
		m.screen = "nodesResult"
		if strings.HasPrefix(m.nodeAction, "node-install-") {
			m.screen = "nodeInstallResult"
			if m.nodeInstallJob == nil && m.nodeAction == "node-install-add" {
				m.prepareNodeInstallInputs()
				m.screen = "nodeInstallInput"
			}
		}
		m.panelResult = ""
		m.problem = msg.err.Error()
		return m, nil
	}
	if len(msg.result.Command) > 0 {
		cmd := exec.CommandContext(m.ctx, msg.result.Command[0], msg.result.Command[1:]...)
		run := m.run
		if msg.action == "node-install-auth" {
			return m, tea.ExecProcess(cmd, func(err error) tea.Msg {
				return nodeResultMsg{run, "node-install-authenticated", services.NodeResult{Install: msg.result.Install}, err}
			})
		}
		return m, tea.ExecProcess(cmd, func(err error) tea.Msg {
			return nodeResultMsg{run, "ssh-exited", services.NodeResult{Text: "SSH session ended.", TextRU: "SSH-сессия завершена."}, err}
		})
	}
	if strings.HasPrefix(msg.action, "node-install-") {
		return m.receiveNodeInstall(msg)
	}
	switch msg.action {
	case "nodes-list":
		m.nodeList = msg.result.Nodes
		m.nodeCursor = min(m.nodeCursor, max(0, len(m.nodeList)-1))
		m.screen = "nodesList"
	case "node-ssh-settings":
		m.nodeSSH = msg.result.SSH
		m.prepareNodeSSHInputs()
		m.screen = "nodesInput"
		return m, m.inputs[0].Focus()
	case "node-ssh-prepare":
		m.nodeSSH = msg.result.SSH
		m.nodeFingerprint = msg.result.Fingerprint
		m.nodeSSHChanged = msg.result.HostKeyChanged
		m.inputs = []textinput.Model{newInput("SHA256:…", "")}
		m.inputs[0].CharLimit = 128
		m.inputs[0].Width = max(1, min(60, m.width-12))
		m.focus = 0
		m.screen = "nodesTrust"
		return m, m.inputs[0].Focus()
	default:
		m.screen = "nodesResult"
		m.panelResult = msg.result.Text
		if m.cfg.Language != "en" && msg.result.TextRU != "" {
			m.panelResult = msg.result.TextRU
		}
	}
	return m, nil
}
func (m *Model) prepareNodeSSHInputs() {
	m.inputs = []textinput.Model{newInput("node.example.com", m.nodeSSH.Host), newInput("22", strconv.Itoa(m.nodeSSH.Port)), newInput(m.l("Пусто — SSH agent / стандартные ключи / пароль", "Empty — SSH agent / default keys / password"), m.nodeSSH.IdentityFile)}
	for i := range m.inputs {
		m.inputs[i].Width = max(1, min(60, m.width-12))
	}
	m.focus = 0
}
func (m Model) updateNodeScreen(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	switch m.screen {
	case "nodesList":
		switch key {
		case "up", "shift+tab":
			if len(m.nodeList) > 0 {
				m.nodeCursor = (m.nodeCursor + len(m.nodeList) - 1) % len(m.nodeList)
			}
		case "down", "tab":
			if len(m.nodeList) > 0 {
				m.nodeCursor = (m.nodeCursor + 1) % len(m.nodeList)
			}
		case "r", "R":
			return m.startNodeRequest(services.NodeRequest{Action: "nodes-list"})
		case "left":
			m.back()
		case "enter", "right":
			if len(m.nodeList) == 0 {
				return m, nil
			}
			m.nodeTarget = m.nodeList[m.nodeCursor]
			m.nodeRequest = services.NodeRequest{Action: m.nodeAction, UUID: m.nodeTarget.UUID}
			m.problem = ""
			m.scroll = 0
			switch m.nodeAction {
			case "nodes-list", "node-status":
				m.nodeRequest.Action = "node-status"
				return m.startNodeRequest(m.nodeRequest)
			case "node-rename":
				m.inputs = []textinput.Model{newInput(m.l("Новое имя", "New name"), m.nodeTarget.Name)}
				m.inputs[0].CharLimit = 30
				m.focus = 0
				m.screen = "nodesInput"
				return m, m.inputs[0].Focus()
			case "node-toggle":
				m.nodeRequest.Value = "disable"
				if m.nodeTarget.IsDisabled {
					m.nodeRequest.Value = "enable"
				}
				m.screen = "nodesConfirm"
			case "node-restart":
				m.screen = "nodesConfirm"
			case "node-ssh":
				m.nodeRequest.Action = "node-ssh-settings"
				return m.startNodeRequest(m.nodeRequest)
			}
		}
	case "nodesInput":
		if key == "tab" || key == "shift+tab" || key == "up" || key == "down" {
			m.inputs[m.focus].Blur()
			delta := 1
			if key == "shift+tab" || key == "up" {
				delta = len(m.inputs) - 1
			}
			m.focus = (m.focus + delta) % len(m.inputs)
			return m, m.inputs[m.focus].Focus()
		}
		if key == "enter" {
			if m.focus < len(m.inputs)-1 {
				m.inputs[m.focus].Blur()
				m.focus++
				return m, m.inputs[m.focus].Focus()
			}
			if m.nodeAction == "node-rename" {
				name := strings.TrimSpace(m.inputs[0].Value())
				if err := installer.ValidateNodeName(name); err != nil {
					m.problem = err.Error()
					return m, nil
				}
				m.nodeRequest = services.NodeRequest{Action: "node-rename", UUID: m.nodeTarget.UUID, Value: name}
				m.screen = "nodesConfirm"
				m.problem = ""
			} else {
				port, err := strconv.Atoi(strings.TrimSpace(m.inputs[1].Value()))
				if err != nil {
					m.problem = m.l("Неверный SSH-порт.", "Invalid SSH port.")
					return m, nil
				}
				settings := services.NodeSSH{Host: strings.TrimSpace(m.inputs[0].Value()), Port: port, IdentityFile: strings.TrimSpace(m.inputs[2].Value())}
				if err := installer.ValidateNodeSSH(settings); err != nil {
					m.problem = err.Error()
					return m, nil
				}
				return m.startNodeRequest(services.NodeRequest{Action: "node-ssh-prepare", UUID: m.nodeTarget.UUID, SSH: settings})
			}
			return m, nil
		}
		var cmd tea.Cmd
		m.inputs[m.focus], cmd = m.inputs[m.focus].Update(msg)
		return m, cmd
	case "nodesTrust":
		if key == "enter" {
			if strings.TrimSpace(m.inputs[0].Value()) != m.nodeFingerprint {
				m.problem = m.l("Введите полный отпечаток, сверенный через консоль хостинга.", "Enter the full fingerprint verified through your provider's console.")
				return m, nil
			}
			if strings.HasPrefix(m.nodeAction, "node-install-") {
				m.problem = ""
				m.screen = "nodeInstallConfirm"
				return m, nil
			}
			m.nodeRequest = services.NodeRequest{Action: "node-ssh-trust", UUID: m.nodeTarget.UUID, SSH: m.nodeSSH, Fingerprint: m.nodeFingerprint}
			m.problem = ""
			m.screen = "nodesConfirm"
			return m, nil
		}
		var cmd tea.Cmd
		m.inputs[0], cmd = m.inputs[0].Update(msg)
		return m, cmd
	case "nodesConfirm":
		if key == "enter" {
			return m.startNodeRequest(m.nodeRequest)
		}
	case "nodesResult":
		if key == "enter" {
			return m.startNodeRequest(services.NodeRequest{Action: "nodes-list"})
		}
	}
	return m, nil
}

func (m Model) nodesView() string {
	title := accent.Render(m.nodeTitle)
	switch m.screen {
	case "nodesProgress":
		return title + "\n\n" + m.l("Выполняется запрос…", "Request in progress…")
	case "nodesList":
		rows := []string{title, ""}
		if len(m.nodeList) == 0 {
			rows = append(rows, m.l("В панели пока нет нод.", "No nodes in the panel yet."))
		}
		for i, node := range m.nodeList {
			status := m.l("не в сети", "offline")
			if node.IsDisabled {
				status = m.l("отключена", "disabled")
			} else if node.IsConnected {
				status = m.l("в сети", "online")
			}
			line := node.Name + " · " + node.Address + " · " + status
			if i == m.nodeCursor {
				line = accent.Render("› " + line)
			} else {
				line = "  " + line
			}
			rows = append(rows, line)
		}
		return strings.Join(rows, "\n")
	case "nodesInput":
		rows := []string{title, "", m.nodeTarget.Name + " · " + m.nodeTarget.Address, ""}
		labels := []string{m.l("Новое имя", "New name")}
		if m.nodeAction == "node-ssh" {
			labels = []string{m.l("SSH-адрес", "SSH address"), m.l("SSH-порт", "SSH port"), m.l("Путь к приватному ключу (необязательно)", "Private key path (optional)")}
		}
		for i, label := range labels {
			rows = append(rows, label, m.inputs[i].View())
		}
		if m.nodeAction == "node-ssh" {
			rows = append(rows, "", m.l("Пароль при необходимости запросит OpenSSH. Он не сохраняется.", "OpenSSH will ask for a password if needed. It is not saved."))
		}
		return strings.Join(rows, "\n")
	case "nodesTrust":
		text := title + "\n\n" + m.nodeSSH.Host + ":" + strconv.Itoa(m.nodeSSH.Port) + "\n" + accent.Render(m.nodeFingerprint) + "\n\n"
		if m.nodeSSHChanged {
			text += warning.Render(m.l("КЛЮЧ ИЛИ АДРЕС СЕРВЕРА ИЗМЕНИЛСЯ", "SERVER KEY OR ADDRESS HAS CHANGED")) + "\n\n"
		}
		return text + m.l("Сверьте отпечаток через консоль хостинга.\nВведите полный SHA256:… для подтверждения:", "Verify the fingerprint through your provider's console.\nEnter the full SHA256:… to confirm:") + "\n" + m.inputs[0].View()
	case "nodesConfirm":
		text := title + "\n\n" + m.nodeTarget.Name + " · " + m.nodeTarget.Address + "\nUUID: " + m.nodeTarget.UUID + "\n\n"
		switch m.nodeAction {
		case "node-rename":
			text += m.l("Новое имя: ", "New name: ") + m.nodeRequest.Value
		case "node-toggle":
			if m.nodeRequest.Value == "enable" {
				text += m.l("Включить управление нодой в панели?", "Enable node management in the panel?")
			} else {
				text += m.l("Отключить управление нодой в панели?", "Disable node management in the panel?")
			}
			text += "\n\n" + warning.Render(m.l("Это меняет состояние в панели. Контейнер и трафик на сервере не останавливаются.", "This changes panel state. The remote container and traffic are not stopped."))
		case "node-restart":
			text += warning.Render(m.l("Панель отправит запрос принудительного перезапуска Xray на выбранной ноде.", "The panel will request a forced Xray restart on this node."))
		case "node-ssh":
			text += m.l("Открыть интерактивный SSH от root:\n", "Open an interactive SSH session as root:\n") + m.nodeSSH.Host + ":" + strconv.Itoa(m.nodeSSH.Port) + "\n" + m.nodeFingerprint
		}
		return text + "\n\n" + m.l("Enter — подтвердить · Esc — назад", "Enter — confirm · Esc — back")
	case "nodesResult":
		return title + "\n\n" + m.panelResult + "\n\n" + m.l("Enter — обновить список · Esc — назад", "Enter — refresh list · Esc — back")
	}
	return ""
}
