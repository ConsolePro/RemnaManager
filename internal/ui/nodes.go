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
	m.nodeConnectWizard = false
	m.nodeBrowser = selected.action == "nodes-list"
	m.nodeTarget = services.Node{}
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
		if request.Action == "node-update-run" || request.Action == "node-reinstall-run" || request.Action == "node-site-run" || request.Action == "node-core-run" || request.Action == "node-core-rollback-run" {
			timeout = 10 * time.Minute
		}
		if request.Action == "node-install-confirm" {
			timeout = 90 * time.Second
		}
		if strings.HasPrefix(request.Action, "node-warp-") && strings.HasSuffix(request.Action, "-run") {
			timeout = 15 * time.Minute
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
	if msg.result.Fingerprint != "" {
		m.nodeTrustFirst = false
	}
	if msg.result.Install != nil {
		m.nodeInstallJob = msg.result.Install
		if !(msg.err != nil && msg.action == "node-install-details") {
			m.nodeInstallParams = msg.result.Install.Params
		}
	}
	if msg.err != nil {
		if strings.HasPrefix(msg.action, "node-warp-") && strings.HasSuffix(msg.action, "-authenticated") {
			msg.err = services.LocalizedError("Не удалось войти на ноду по SSH. Повторите операцию и проверьте пароль root.", "Could not authenticate to the node over SSH; retry and check the root password.")
		}
		if msg.action == "node-install-details" {
			m.problem = m.errorText(msg.err)
			m.prepareNodeDetails()
			m.screen = "nodeDetailsInput"
			return m, m.inputs[0].Focus()
		}
		if msg.action == "node-install-authenticated" && m.nodeInstallParams.SSH.IdentityFile == "" {
			msg.err = services.LocalizedError("Не удалось войти по SSH. Проверьте пароль root и разрешён ли вход root по паролю на сервере ноды. Пароль вводится скрыто; ключ от хостинга не нужен.", "SSH login failed. Check the root password and whether root password login is allowed on the node. Password input is hidden; a hosting-provider private key is not required.")
		}
		if msg.action == "node-install-detect-panel-ip" || msg.action == "node-install-panel-ip" {
			m.problem = m.errorText(msg.err)
			value := ""
			if msg.action == "node-install-panel-ip" {
				value = m.nodeRequest.Install.PanelIP
			}
			return m.showNodePanelIP(value)
		}
		m.screen = "nodesResult"
		if strings.HasPrefix(m.nodeAction, "node-install-") {
			m.screen = "nodeInstallResult"
			if m.nodeInstallJob == nil && m.nodeAction == "node-install-add" {
				if m.nodeConnectWizard {
					m.prepareNodeConnection()
					m.screen = "nodeConnectInput"
				} else {
					m.prepareNodeInstallInputs()
					m.screen = "nodeInstallInput"
				}
			}
		}
		m.panelResult = ""
		m.problem = m.errorText(msg.err)
		return m, nil
	}
	if len(msg.result.Command) > 0 {
		cmd := exec.CommandContext(m.ctx, msg.result.Command[0], msg.result.Command[1:]...)
		run := m.run
		if msg.action == "node-install-auth" || msg.action == "node-update-auth" || msg.action == "node-reinstall-auth" || msg.action == "node-site-auth" || msg.action == "node-ssh-auth" || msg.action == "node-core-auth" || msg.action == "node-core-rollback-auth" || msg.action == "node-warp-install-auth" || msg.action == "node-warp-remove-auth" {
			return m, tea.ExecProcess(cmd, func(err error) tea.Msg {
				action := strings.TrimSuffix(msg.action, "-auth") + "-authenticated"
				return nodeResultMsg{run, action, services.NodeResult{Install: msg.result.Install}, err}
			})
		}
		return m, tea.ExecProcess(cmd, func(err error) tea.Msg {
			return nodeResultMsg{run, "ssh-exited", services.NodeResult{Text: "SSH session ended.", TextRU: "SSH-сессия завершена."}, err}
		})
	}
	if strings.HasPrefix(msg.action, "node-install-") {
		return m.receiveNodeInstall(msg)
	}
	if msg.result.NoChange {
		m.screen = "nodesResult"
		m.panelResult = msg.result.Text
		if m.cfg.Language != "en" && msg.result.TextRU != "" {
			m.panelResult = msg.result.TextRU
		}
		return m, nil
	}
	switch msg.action {
	case "node-details":
		if len(msg.result.Nodes) != 1 {
			m.screen, m.problem = "nodesResult", m.l("Панель не вернула данные ноды.", "The panel did not return node details.")
			return m, nil
		}
		m.nodeTarget = msg.result.Nodes[0]
		m.nodeTitle = m.l("Управление нодой", "Manage node")
		m.screen = "nodesCard"
	case "nodes-list":
		m.nodeList = msg.result.Nodes
		m.nodeCursor = min(m.nodeCursor, max(0, m.nodeListCount()-1))
		if m.nodeBrowser {
			m.nodeTitle = m.l("Ноды и расширения", "Nodes and extensions")
		}
		m.screen = "nodesList"
	case "node-ssh-settings":
		m.nodeSSH = msg.result.SSH
		m.prepareNodeSSHInputs()
		m.screen = "nodesInput"
		return m, m.inputs[0].Focus()
	case "node-ssh-auth", "node-ssh-authenticated":
		if msg.result.NeedsSSHSettings {
			m.nodeSSH = msg.result.SSH
			m.prepareNodeSSHInputs()
			m.screen = "nodesInput"
			return m, m.inputs[0].Focus()
		}
		return m.startNodeRequest(services.NodeRequest{Action: "node-ssh-open", UUID: m.nodeTarget.UUID})
	case "node-core-auth", "node-core-authenticated", "node-core-rollback-auth", "node-core-rollback-authenticated":
		return m.startNodeRequest(services.NodeRequest{Action: m.nodeAction + "-run", UUID: m.nodeTarget.UUID, Value: m.nodeRequest.Value})
	case "node-warp-install-auth", "node-warp-install-authenticated", "node-warp-remove-auth", "node-warp-remove-authenticated":
		return m.startNodeRequest(services.NodeRequest{Action: m.nodeAction + "-run", UUID: m.nodeTarget.UUID})
	case "node-warp-install-prepare", "node-warp-remove-prepare":
		m.panelResult = msg.result.Text
		if m.cfg.Language != "en" {
			m.panelResult = msg.result.TextRU
		}
		m.nodeRequest = services.NodeRequest{Action: m.nodeAction + "-auth", UUID: m.nodeTarget.UUID}
		m.screen = "nodesConfirm"
	case "node-site-auth", "node-site-authenticated":
		return m.startNodeRequest(services.NodeRequest{Action: "node-site-run", UUID: m.nodeTarget.UUID, Value: m.nodeRequest.Value})
	case "node-update-auth", "node-reinstall-auth", "node-update-authenticated", "node-reinstall-authenticated":
		return m.startNodeRequest(services.NodeRequest{Action: strings.TrimSuffix(strings.TrimSuffix(msg.action, "-authenticated"), "-auth") + "-run", UUID: m.nodeTarget.UUID})
	case "node-core-check", "node-core-prepare", "node-core-rollback-prepare", "node-ssh-prepare", "node-update-prepare", "node-reinstall-prepare":
		if msg.action == "node-core-check" || msg.action == "node-core-prepare" || msg.action == "node-core-rollback-prepare" || msg.action == "node-update-prepare" || msg.action == "node-reinstall-prepare" {
			m.panelResult = msg.result.Text
			if m.cfg.Language != "en" && msg.result.TextRU != "" {
				m.panelResult = msg.result.TextRU
			}
		}
		if (msg.action == "node-core-check" || msg.action == "node-core-rollback-prepare") && len(msg.result.Versions) > 0 {
			m.nodeAction = "node-core-rollback"
			m.nodeCoreVersions = msg.result.Versions
			m.nodeCoreCursor = 0
			m.screen = "nodesCoreVersions"
			return m, nil
		}
		if msg.action == "node-core-check" {
			m.nodeAction = "node-core"
		}
		if msg.action != "node-ssh-prepare" {
			m.nodeRequest = services.NodeRequest{Action: m.nodeAction + "-auth", UUID: m.nodeTarget.UUID, Value: msg.result.Version}
			m.screen = "nodesConfirm"
			return m, nil
		}
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
		if msg.action == "node-delete" && m.nodeBrowser {
			for i, node := range m.nodeList {
				if node.UUID == m.nodeTarget.UUID {
					m.nodeList = append(m.nodeList[:i], m.nodeList[i+1:]...)
					break
				}
			}
			m.nodeTarget = services.Node{}
			m.nodeCursor = min(m.nodeCursor, max(0, m.nodeListCount()-1))
		}
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
	case "nodesCard":
		actions := nodeCardActions()
		switch key {
		case "up", "shift+tab":
			m.nodeActionCursor = (m.nodeActionCursor + len(actions) - 1) % len(actions)
		case "down", "tab":
			m.nodeActionCursor = (m.nodeActionCursor + 1) % len(actions)
		case "r", "R":
			return m.startNodeRequest(services.NodeRequest{Action: "node-details", UUID: m.nodeTarget.UUID})
		case "enter", "right":
			return m.beginNodeAction(actions[m.nodeActionCursor])
		}
	case "nodesCoreVersions":
		if len(m.nodeCoreVersions) == 0 {
			return m, nil
		}
		switch key {
		case "up", "shift+tab":
			m.nodeCoreCursor = (m.nodeCoreCursor + len(m.nodeCoreVersions) - 1) % len(m.nodeCoreVersions)
		case "down", "tab":
			m.nodeCoreCursor = (m.nodeCoreCursor + 1) % len(m.nodeCoreVersions)
		case "enter", "right":
			if len(m.nodeCoreVersions) > 0 {
				version := m.nodeCoreVersions[m.nodeCoreCursor]
				m.nodeRequest = services.NodeRequest{Action: "node-core-rollback-auth", UUID: m.nodeTarget.UUID, Value: version}
				m.screen = "nodesConfirm"
			}
		}
	case "nodesList":
		switch key {
		case "up", "shift+tab":
			if m.nodeListCount() > 0 {
				m.nodeCursor = (m.nodeCursor + m.nodeListCount() - 1) % m.nodeListCount()
			}
		case "down", "tab":
			if m.nodeListCount() > 0 {
				m.nodeCursor = (m.nodeCursor + 1) % m.nodeListCount()
			}
		case "r", "R":
			return m.startNodeRequest(services.NodeRequest{Action: "nodes-list"})
		case "left":
			m.back()
		case "enter", "right":
			if m.nodeBrowser {
				if m.nodeCursor >= len(m.nodeList) {
					action := "node-install-add"
					if m.nodeCursor > len(m.nodeList) {
						action = "node-install-pending"
					}
					m.nodeBrowser = false
					return m.openNodeOperation(operation("Ноды", "Nodes", action))
				}
				m.nodeTarget = m.nodeList[m.nodeCursor]
				m.nodeActionCursor = 0
				return m.startNodeRequest(services.NodeRequest{Action: "node-details", UUID: m.nodeTarget.UUID})
			}
			if len(m.nodeList) == 0 {
				return m, nil
			}
			m.nodeTarget = m.nodeList[m.nodeCursor]
			m.nodeRequest = services.NodeRequest{Action: m.nodeAction, UUID: m.nodeTarget.UUID}
			m.problem = ""
			m.scroll = 0
			return m.beginNodeAction(operation(m.nodeTitle, m.nodeTitle, m.nodeAction))
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
			if m.nodeAction == "node-site" {
				name := strings.TrimSpace(m.inputs[0].Value())
				found := false
				for _, template := range installer.NodeSiteTemplates {
					found = found || name == template
				}
				if !found {
					m.problem = "studio / atlas / journal"
					return m, nil
				}
				m.nodeRequest = services.NodeRequest{Action: "node-site-auth", UUID: m.nodeTarget.UUID, Value: name}
				m.screen = "nodesConfirm"
			} else if m.nodeAction == "node-rename" {
				name := strings.TrimSpace(m.inputs[0].Value())
				if err := installer.ValidateNodeName(name); err != nil {
					m.problem = m.errorText(err)
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
					m.problem = m.errorText(err)
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
		if key == "tab" && m.nodeConnectWizard && m.nodeInstallJob == nil && !m.nodeSSHChanged {
			m.nodeTrustFirst = !m.nodeTrustFirst
			if m.nodeTrustFirst {
				m.inputs[0].Blur()
			} else {
				return m, m.inputs[0].Focus()
			}
			return m, nil
		}
		if key == "enter" {
			if !m.nodeTrustFirst && strings.TrimSpace(m.inputs[0].Value()) != m.nodeFingerprint {
				m.problem = m.l("Введите полный SHA256-отпечаток SSH-сервера после проверки. Это не приватный ключ.", "Enter the full verified SHA256 server fingerprint. This is not a private key.")
				return m, nil
			}
			if strings.HasPrefix(m.nodeAction, "node-install-") {
				m.problem = ""
				if m.nodeConnectWizard && m.nodeInstallJob == nil {
					return m.startNodeRequest(services.NodeRequest{Action: "node-install-connect-create", Install: m.nodeInstallParams, Fingerprint: m.nodeFingerprint})
				}
				m.screen = "nodeInstallConfirm"
				return m, nil
			}
			if m.nodeAction == "node-update" || m.nodeAction == "node-reinstall" {
				m.nodeRequest = services.NodeRequest{Action: m.nodeAction + "-auth", UUID: m.nodeTarget.UUID, Fingerprint: m.nodeFingerprint}
				m.problem = ""
				m.screen = "nodesConfirm"
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
			if m.nodeBrowser {
				if m.nodeAction == "node-delete" && m.problem == "" {
					m.nodeTarget = services.Node{}
					m.nodeAction = "nodes-list"
					return m.startNodeRequest(services.NodeRequest{Action: "nodes-list"})
				}
				if m.nodeTarget.UUID != "" {
					return m.startNodeRequest(services.NodeRequest{Action: "node-details", UUID: m.nodeTarget.UUID})
				}
			}
			return m.startNodeRequest(services.NodeRequest{Action: "nodes-list"})
		}
	}
	return m, nil
}

func (m Model) nodesView() string {
	title := accent.Render(m.nodeTitle)
	switch m.screen {
	case "nodesCard":
		rows := append(m.nodeCardInfo(), "")
		for i, action := range nodeCardActions() {
			line := "  " + m.l(action.ru, action.en)
			if i == m.nodeActionCursor {
				line = accent.Render("› " + m.l(action.ru, action.en))
			}
			rows = append(rows, line)
		}
		return strings.Join(rows, "\n")
	case "nodesCoreVersions":
		rows := []string{title, "", m.panelResult, "", m.l("Выберите версию для отката:", "Select a version for rollback:")}
		// Keep the selected release visible even in a small terminal.
		start := max(0, m.nodeCoreCursor-3)
		for i := start; i < min(len(m.nodeCoreVersions), start+7); i++ {
			line := "  " + m.nodeCoreVersions[i]
			if i == m.nodeCoreCursor {
				line = accent.Render("› " + m.nodeCoreVersions[i])
			}
			rows = append(rows, line)
		}
		return strings.Join(rows, "\n")
	case "nodesProgress":
		if m.nodeAction == "node-reinstall" {
			return title + "\n\n" + m.l("Пересоздание контейнера ноды…\nСохранённые ключи и версия образа сохраняются.\nПри ошибке будет выполнена попытка отката.\nДля восстановления прерванной операции выберите этот пункт повторно.", "Recreating the node container…\nSaved keys and the pinned image version are retained.\nFailure triggers a rollback attempt.\nSelect this operation again to recover an interrupted attempt.")
		}
		if m.nodeAction == "node-update" {
			return title + "\n\n" + m.l("Проверка и обновление ноды…\nПри прерывании обновления сработает автоматический откат.\nПовторно выберите обновление, чтобы проверить восстановление.", "Checking and updating node…\nAn interrupted update will roll back automatically.\nSelect Update again to check recovery.")
		}
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
		if m.nodeBrowser {
			for i, text := range []string{m.l("Добавить ноду", "Add node"), m.l("Незавершённые установки", "Pending installations")} {
				line := "  " + text
				if m.nodeCursor == len(m.nodeList)+i {
					line = accent.Render("› " + text)
				}
				rows = append(rows, line)
			}
		}
		return strings.Join(rows, "\n")
	case "nodesInput":
		rows := []string{title, "", m.nodeTarget.Name + " · " + m.nodeTarget.Address, ""}
		labels := []string{m.l("Новое имя", "New name")}
		if m.nodeAction == "node-site" {
			labels = []string{m.l("Шаблон сайта: studio / atlas / journal", "Website template: studio / atlas / journal")}
		}
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
		text += m.l("Отпечаток — идентификатор SSH-сервера, не пароль и не ваш ключ.\nЕсли уже подключались к ноде из своего терминала, выполните там:\nssh-keygen -lf /etc/ssh/ssh_host_ed25519_key.pub\nСравните SHA256:… с показанным выше и введите его ниже.\nПри отсутствии такого доступа можно проверить через консоль провайдера.\n", "The fingerprint identifies the SSH server; it is not a password or your private key.\nIf you already have a trusted terminal session to the node, run there:\nssh-keygen -lf /etc/ssh/ssh_host_ed25519_key.pub\nCompare SHA256:… with the value above and enter it below.\nAlternatively, check it through the provider console.\n")
		if m.nodeConnectWizard && m.nodeInstallJob == nil && !m.nodeSSHChanged {
			choice := m.l("Сверить и ввести отпечаток", "Verify and enter fingerprint")
			if m.nodeTrustFirst {
				choice = m.l("Первое подключение: довериться показанному ключу", "First connection: trust the displayed key")
			}
			text += "\n" + accent.Render(choice) + "\n" + m.l("Нет проверенного отпечатка? Tab — первое доверие · Enter — подтвердить.\nПервое доверие сохраняет ключ без независимой проверки сервера.\nИзменение сохранённого ключа потребует проверки.\n", "No verified fingerprint? Tab — first-use trust · Enter — confirm.\nFirst-use trust saves the key without independent verification.\nChanges to the saved key require verification.\n")
		}
		if !m.nodeTrustFirst {
			text += "\n" + m.inputs[0].View()
		}
		return text
	case "nodesConfirm":
		text := title + "\n\n" + m.nodeTarget.Name + " · " + m.nodeTarget.Address + "\nUUID: " + m.nodeTarget.UUID + "\n\n"
		switch m.nodeAction {
		case "node-warp-install", "node-warp-remove":
			text += m.panelResult
		case "node-delete":
			text += warning.Render(m.l("Удалить выбранную ноду из панели?\nКонтейнер на сервере продолжит работать. Профили, хосты и сквады сохраняются.", "Delete the selected node from the panel?\nThe remote container will remain running. Profiles, hosts and squads are retained."))
		case "node-core", "node-core-rollback":
			if m.nodeAction == "node-core-rollback" {
				text += m.l("Откат Xray до ", "Roll back Xray to ") + m.nodeRequest.Value + "\n"
			}
			text += m.panelResult + "\n\n" + warning.Render(m.l("Будет установлено только выбранное ядро Xray. Образ ноды и профиль сохраняются.\nКонтейнер перезапустится; клиентские соединения могут оборваться.\nПри ошибке проверки версии, связи с панелью или HTTPS выполняется откат.", "Only the selected Xray core will be installed. The node image and profile are retained.\nThe container will restart; client connections may drop.\nFailure to verify the version, panel connection or HTTPS triggers rollback."))
		case "node-site":
			text += m.l("Новый шаблон: ", "New template: ") + m.nodeRequest.Value + "\n\n" + m.l("Будут заменены только страницы сайта. После проверки HTTPS шаблон сохранится автоматически; при ошибке вернётся прежний сайт.", "Website pages will be replaced. HTTPS verification commits the template automatically; failure restores the previous website.")
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
		case "node-update":
			text += m.panelResult + "\n\n" + warning.Render(m.l("Будет загружен официальный remnawave/node:latest и закреплён его digest.\nКонтейнер перезапустится; соединения клиентов могут оборваться.\nПри ошибке будет восстановлен прежний Compose и образ.\nПрерванное обновление сначала будет восстановлено без новой установки.", "The official remnawave/node:latest will be pulled and pinned by digest.\nThe container will restart; client connections may be interrupted.\nFailure restores the previous Compose and image.\nAn interrupted update is recovered before attempting another update."))
		case "node-reinstall":
			text += m.panelResult + "\n\n" + warning.Render(m.l("Контейнер ноды будет пересоздан из сохранённого образа.\nКлючи, настройки Compose и объекты панели сохраняются.\nСайт, сертификаты и firewall не переустанавливаются.\nСоединения клиентов могут оборваться. При ошибке — попытка отката.\nПрерванная операция сначала будет восстановлена.", "The node container will be recreated from the pinned image.\nKeys, Compose settings and panel resources are retained.\nThe website, certificates and firewall are retained.\nClient connections may drop. Failure triggers a rollback attempt.\nAn interrupted operation is recovered first."))
		}
		return text + "\n\n" + m.l("Enter — подтвердить · Esc — назад", "Enter — confirm · Esc — back")
	case "nodesResult":
		if m.nodeBrowser {
			return title + "\n\n" + m.panelResult + "\n\n" + m.l("Enter — обновить данные · Esc — назад", "Enter — refresh details · Esc — back")
		}
		return title + "\n\n" + m.panelResult + "\n\n" + m.l("Enter — обновить список · Esc — назад", "Enter — refresh list · Esc — back")
	}
	return ""
}
