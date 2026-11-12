package ui

import (
	"encoding/json"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/ConsolePro/RemnaManager/internal/services"
	tea "github.com/charmbracelet/bubbletea"
)

func nodeCardActions() []item {
	return []item{
		operation("Шаблон selfsteal", "Selfsteal template", "node-site"),
		operation("Ядро Xray", "Xray core", "node-core-check"),
		operation("Переименовать", "Rename", "node-rename"),
		operation("Переустановить", "Reinstall", "node-reinstall"),
		operation("Обновить ноду", "Update node", "node-update"),
		operation("Включить / отключить в панели", "Enable / disable in panel", "node-toggle"),
		operation("Перезапустить Xray", "Restart Xray", "node-restart"),
		operation("SSH-подключение", "SSH connection", "node-ssh"),
		operation("Удалить", "Delete", "node-delete"),
	}
}

func (m Model) nodeListCount() int {
	if m.nodeBrowser {
		return len(m.nodeList) + 2
	}
	return len(m.nodeList)
}

func (m Model) nodeCardInfo() []string {
	n := m.nodeTarget
	status := m.l("не в сети", "offline")
	if n.IsDisabled {
		status = m.l("отключена в панели", "disabled in panel")
	} else if n.IsConnected {
		status = m.l("в сети", "online")
	}
	version := func(v string) string {
		if strings.TrimSpace(v) == "" {
			return "—"
		}
		return v
	}
	var serverUptime *json.Number
	if n.System != nil {
		serverUptime = n.System.Stats.Uptime
	}
	return []string{
		accent.Render(n.Name),
		m.l("Адрес: ", "Address: ") + n.Address + " · " + m.l("Статус: ", "Status: ") + status,
		"Xray: " + version(n.CoreVersion()) + " · Remna Node: " + version(n.AgentVersion()),
		m.l("Аптайм Xray: ", "Xray uptime: ") + m.nodeUptime(n.XrayUptime),
		m.l("Аптайм сервера: ", "Server uptime: ") + m.nodeUptime(serverUptime),
	}
}

func (m Model) nodeUptime(value *json.Number) string {
	if value == nil {
		return "—"
	}
	seconds, err := strconv.ParseFloat(string(*value), 64)
	if err != nil || math.IsNaN(seconds) || math.IsInf(seconds, 0) || seconds < 0 || seconds > 1e10 {
		return "—"
	}
	d := time.Duration(seconds) * time.Second
	days := int(d / (24 * time.Hour))
	return strconv.Itoa(days) + m.l(" д ", "d ") + strconv.Itoa(int(d/time.Hour)%24) + m.l(" ч ", "h ") + strconv.Itoa(int(d/time.Minute)%60) + m.l(" мин", "m")
}

func (m Model) beginNodeAction(selected item) (tea.Model, tea.Cmd) {
	m.nodeAction, m.nodeTitle = selected.action, m.l(selected.ru, selected.en)
	m.problem, m.panelResult, m.scroll = "", "", 0
	m.nodeRequest = services.NodeRequest{Action: m.nodeAction, UUID: m.nodeTarget.UUID}
	switch m.nodeAction {
	case "node-rename":
		m.inputs = append(m.inputs[:0], newInput(m.l("Новое имя", "New name"), m.nodeTarget.Name))
		m.inputs[0].CharLimit, m.focus, m.screen = 30, 0, "nodesInput"
		return m, m.inputs[0].Focus()
	case "node-site":
		m.inputs = append(m.inputs[:0], newInput("studio / atlas / journal", "studio"))
		m.focus, m.screen = 0, "nodesInput"
		return m, m.inputs[0].Focus()
	case "node-toggle":
		m.nodeRequest.Value = "disable"
		if m.nodeTarget.IsDisabled {
			m.nodeRequest.Value = "enable"
		}
		m.screen = "nodesConfirm"
	case "node-delete":
		m.nodeRequest.Value = m.nodeTarget.UUID
		m.screen = "nodesConfirm"
	case "node-restart":
		m.screen = "nodesConfirm"
	case "node-ssh":
		return m.startNodeRequest(services.NodeRequest{Action: "node-ssh-auth", UUID: m.nodeTarget.UUID})
	case "node-core-check":
		return m.startNodeRequest(m.nodeRequest)
	case "node-core", "node-core-rollback", "node-update", "node-reinstall":
		return m.startNodeRequest(services.NodeRequest{Action: m.nodeAction + "-prepare", UUID: m.nodeTarget.UUID})
	case "nodes-list", "node-status":
		return m.startNodeRequest(services.NodeRequest{Action: "node-status", UUID: m.nodeTarget.UUID})
	}
	return m, nil
}
