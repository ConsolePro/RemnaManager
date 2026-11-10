package ui

import (
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/ConsolePro/RemnaManager/internal/installer"
	"github.com/ConsolePro/RemnaManager/internal/services"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
)

func (m Model) openNodeInstall(selected item) (tea.Model, tea.Cmd) {
	m.cancelInstall()
	m.run++
	m.nodeAction = selected.action
	m.nodeTitle = m.l(selected.ru, selected.en)
	m.nodeInstallJob = nil
	m.nodeInstallJobs = nil
	m.nodeCursor = 0
	m.problem = ""
	m.scroll = 0
	m.panelResult = ""
	if selected.action == "node-install-pending" {
		return m.startNodeRequest(services.NodeRequest{Action: "node-install-pending"})
	}
	m.nodeInstallParams = services.NodeInstallParameters{SSH: services.NodeSSH{Port: 22}}
	m.prepareNodeInstallInputs()
	m.screen = "nodeInstallInput"
	return m, m.inputs[0].Focus()
}
func (m *Model) prepareNodeInstallInputs() {
	p := m.nodeInstallParams
	m.inputs = []textinput.Model{newInput("Node-01", p.Name), newInput("node.example.com", p.Address), newInput("22", strconv.Itoa(p.SSH.Port)), newInput("cover.example.com", p.Domain), newInput(m.l("Пусто — определить автоматически по SSH", "Empty — detect automatically over SSH"), p.PanelIP), newInput(m.l("Пусто — SSH agent / стандартный ключ / пароль", "Empty — SSH agent / default key / password"), p.SSH.IdentityFile)}
	for i := range m.inputs {
		m.inputs[i].Width = max(1, min(60, m.width-12))
	}
	m.inputs[0].CharLimit = 30
	m.focus = 0
}
func (m Model) receiveNodeInstall(msg nodeResultMsg) (tea.Model, tea.Cmd) {
	switch msg.action {
	case "node-install-pending":
		m.nodeInstallJobs = msg.result.Jobs
		m.nodeCursor = min(m.nodeCursor, max(0, len(m.nodeInstallJobs)-1))
		m.screen = "nodeInstallPending"
	case "node-install-scan", "node-install-rescan":
		m.nodeSSH = msg.result.SSH
		m.nodeFingerprint = msg.result.Fingerprint
		m.nodeSSHChanged = msg.result.HostKeyChanged
		m.inputs = []textinput.Model{newInput("SHA256:…", "")}
		m.inputs[0].Width = max(1, min(60, m.width-12))
		m.inputs[0].CharLimit = 128
		m.focus = 0
		m.screen = "nodesTrust"
		return m, m.inputs[0].Focus()
	case "node-install-load":
		return m.startNodeRequest(services.NodeRequest{Action: "node-install-rescan", JobID: m.nodeInstallJob.ID})
	case "node-install-create":
		return m.startNodeRequest(services.NodeRequest{Action: "node-install-auth", JobID: m.nodeInstallJob.ID, Fingerprint: m.nodeFingerprint})
	case "node-install-authenticated":
		if m.nodeInstallParams.PanelIP == "" {
			return m.startNodeRequest(services.NodeRequest{Action: "node-install-detect-panel-ip", JobID: m.nodeInstallJob.ID})
		}
		return m.startNodeInstallStages()
	case "node-install-detect-panel-ip":
		return m.showNodePanelIP(msg.result.Text)
	case "node-install-panel-ip":
		return m.startNodeInstallStages()
	case "node-install-step":
		if m.nodeInstallStage < 7 {
			m.nodeInstallStage++
			return m.startNodeRequest(services.NodeRequest{Action: "node-install-step", JobID: m.nodeInstallJob.ID, Stage: m.nodeInstallStage})
		}
		m.screen = "nodeInstallVerify"
	case "node-install-confirm":
		m.screen = "nodeInstallResult"
		m.panelResult = m.l("Нода подключена, firewall сохранён.\nПрофиль Reality, хост подписки и internal squad созданы.\nНазначьте squad пользователям в панели.", "Node connected and firewall committed.\nReality profile, subscription host and internal squad created.\nAssign the squad to users in the panel.")
	}
	return m, nil
}
func (m Model) startNodeInstallStages() (tea.Model, tea.Cmd) {
	m.nodeInstallStage = 0
	return m.startNodeRequest(services.NodeRequest{Action: "node-install-step", JobID: m.nodeInstallJob.ID, Stage: 0})
}
func (m Model) showNodePanelIP(value string) (tea.Model, tea.Cmd) {
	m.screen = "nodeInstallPanelIP"
	m.inputs = []textinput.Model{newInput(m.l("IP панели, видимый с ноды", "Panel IP reachable from the node"), value)}
	m.inputs[0].Width = max(1, min(60, m.width-12))
	m.focus = 0
	return m, m.inputs[0].Focus()
}
func (m Model) updateNodeInstallScreen(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	switch m.screen {
	case "nodeInstallPanelIP":
		if key == "enter" {
			value := strings.TrimSpace(m.inputs[0].Value())
			ip := net.ParseIP(value)
			if ip == nil || !ip.IsGlobalUnicast() {
				m.problem = m.l("Укажите IP панели, доступный с ноды.", "Enter the panel IP reachable from the node.")
				return m, nil
			}
			return m.startNodeRequest(services.NodeRequest{Action: "node-install-panel-ip", JobID: m.nodeInstallJob.ID, Install: services.NodeInstallParameters{PanelIP: value}})
		}
		var cmd tea.Cmd
		m.inputs[0], cmd = m.inputs[0].Update(msg)
		return m, cmd
	case "nodeInstallInput":
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
			port, err := strconv.Atoi(strings.TrimSpace(m.inputs[2].Value()))
			if err != nil {
				m.problem = m.l("Неверный SSH-порт.", "Invalid SSH port.")
				return m, nil
			}
			p := services.NodeInstallParameters{Name: strings.TrimSpace(m.inputs[0].Value()), Address: strings.ToLower(strings.TrimSpace(m.inputs[1].Value())), Domain: strings.ToLower(strings.TrimSpace(m.inputs[3].Value())), PanelIP: strings.TrimSpace(m.inputs[4].Value())}
			p.SSH = services.NodeSSH{Host: p.Address, Port: port, IdentityFile: strings.TrimSpace(m.inputs[5].Value())}
			if err = installer.ValidateNodeInstall(p); err != nil {
				m.problem = err.Error()
				return m, nil
			}
			m.nodeInstallParams = p
			return m.startNodeRequest(services.NodeRequest{Action: "node-install-scan", Install: p})
		}
		var cmd tea.Cmd
		m.inputs[m.focus], cmd = m.inputs[m.focus].Update(msg)
		return m, cmd
	case "nodeInstallConfirm":
		if key == "enter" {
			if m.nodeInstallJob != nil {
				return m.startNodeRequest(services.NodeRequest{Action: "node-install-auth", JobID: m.nodeInstallJob.ID, Fingerprint: m.nodeFingerprint})
			}
			return m.startNodeRequest(services.NodeRequest{Action: "node-install-create", Install: m.nodeInstallParams, Fingerprint: m.nodeFingerprint})
		}
	case "nodeInstallVerify":
		if key == "enter" {
			return m.startNodeRequest(services.NodeRequest{Action: "node-install-confirm", JobID: m.nodeInstallJob.ID})
		}
	case "nodeInstallPending":
		switch key {
		case "up", "shift+tab":
			if len(m.nodeInstallJobs) > 0 {
				m.nodeCursor = (m.nodeCursor + len(m.nodeInstallJobs) - 1) % len(m.nodeInstallJobs)
			}
		case "down", "tab":
			if len(m.nodeInstallJobs) > 0 {
				m.nodeCursor = (m.nodeCursor + 1) % len(m.nodeInstallJobs)
			}
		case "r", "R":
			return m.startNodeRequest(services.NodeRequest{Action: "node-install-pending"})
		case "enter", "right":
			if len(m.nodeInstallJobs) > 0 {
				selected := m.nodeInstallJobs[m.nodeCursor]
				m.nodeInstallJob = &selected
				return m.startNodeRequest(services.NodeRequest{Action: "node-install-load", JobID: selected.ID})
			}
		}
	case "nodeInstallResult":
		if key == "enter" {
			if m.nodeInstallJob != nil && m.nodeInstallJob.Ready {
				m.screen = "menu"
				m.problem = ""
				return m, nil
			}
			return m.startNodeRequest(services.NodeRequest{Action: "node-install-pending"})
		}
	}
	return m, nil
}

func (m Model) nodeInstallView() string {
	title := accent.Render(m.nodeTitle)
	switch m.screen {
	case "nodeInstallPanelIP":
		return title + "\n\n" + m.l("Адрес панели для firewall ноды", "Panel address for the node firewall") + "\n\n" + m.inputs[0].View() + "\n\n" + m.l("Адрес определён со стороны ноды по SSH-соединению.\nПроверьте его: подключения backend могут использовать другой адрес.\nПри необходимости исправьте IP и нажмите Enter.\nЕсли адрес не определён, введите его вручную.\nУстановка на ноде ещё не началась.", "The address is detected by the node from the SSH connection.\nCheck it: backend connections may use a different address.\nEdit the IP if needed and press Enter.\nIf detection failed, enter the address manually.\nInstallation on the node has not started yet.")
	case "nodeInstallInput":
		labels := [][2]string{{"Имя ноды", "Node name"}, {"Публичный IP / домен ноды и SSH", "Node public IP / DNS and SSH"}, {"SSH-порт", "SSH port"}, {"Домен selfsteal (DNS на эту ноду)", "Selfsteal domain (DNS points to node)"}, {"IP панели (необязательно, автоопределение)", "Panel IP (optional, automatic detection)"}, {"Приватный SSH-ключ (необязательно)", "Private SSH key (optional)"}}
		rows := []string{title, ""}
		for i, label := range labels {
			rows = append(rows, m.l(label[0], label[1]), m.inputs[i].View())
		}
		rows = append(rows, "", m.l("Нужен отдельный VPS с systemd. Пароль запросит OpenSSH; он не сохраняется.", "A dedicated VPS with systemd is required. OpenSSH asks for a password; it is not saved."))
		return strings.Join(rows, "\n")
	case "nodeInstallConfirm":
		p := m.nodeInstallParams
		if p.PanelIP == "" {
			p.PanelIP = m.l("автоматически после входа по SSH", "automatic after SSH authentication")
		}
		text := title + fmt.Sprintf("\n\n%s\n%s · SSH %d\nSelfsteal: %s\nPanel IP: %s\n\n", p.Name, p.Address, p.SSH.Port, p.Domain, p.PanelIP)
		if m.nodeInstallJob != nil {
			text += m.l("Продолжение установки: ", "Resume installation: ") + m.nodeInstallJob.ID + "\n\n"
		}
		return text + warning.Render(m.l("Будут установлены Docker, Nginx, сертификат и Remnawave Node.\nВ панели будут созданы профиль Reality, хост и internal squad.\nНовые входящие порты: SSH, TCP 80/443 и 2222 только с IP панели.\nОстальные порты закроются с таймером отката на 120 секунд.", "Docker, Nginx, a certificate and Remnawave Node will be installed.\nA Reality profile, host and internal squad will be created in the panel.\nNew inbound ports: SSH, TCP 80/443, and 2222 only from the panel IP.\nOther ports will be blocked with a 120-second rollback timer.")) + "\n\n" + m.l("Enter — начать / продолжить · Esc — назад", "Enter — install / resume · Esc — back")
	case "nodeInstallProgress":
		steps := installer.NodeInstallStages()
		rows := []string{title, ""}
		done := 0
		for i, step := range steps {
			prefix := "○ "
			if m.nodeInstallJob != nil && i < len(m.nodeInstallJob.Completed) && m.nodeInstallJob.Completed[i] {
				prefix = "✓ "
				done++
			}
			if i == m.nodeInstallStage {
				prefix = "● "
			}
			rows = append(rows, prefix+m.l(step[0], step[1]))
		}
		rows = append(rows, "", m.classicProgressView(float64(done)/float64(len(steps))))
		if m.nodeInstallJob != nil {
			rows = append(rows, "", m.l("Журнал установки: ", "Installation journal: ")+m.nodeInstallJob.ID)
		}
		return strings.Join(rows, "\n")
	case "nodeInstallVerify":
		text := title + "\n\n" + warning.Render(m.l("ПРОВЕРЬТЕ НОВОЕ SSH-ПОДКЛЮЧЕНИЕ К НОДЕ", "VERIFY A NEW SSH CONNECTION TO THE NODE")) + "\n\n" + m.l("Нода подключена к панели. Откройте вторую SSH-сессию к серверу ноды.\nEnter — подтвердить доступ и сохранить firewall.\nБез подтверждения прежние правила вернутся через 120 секунд.\nEsc — оставить автоматический откат.", "The node is connected. Open a second SSH session to the node server.\nEnter — confirm access and persist the firewall.\nWithout confirmation the previous rules return after 120 seconds.\nEsc — leave automatic rollback armed.")
		if m.nodeInstallJob != nil && !m.nodeInstallJob.RollbackAt.IsZero() {
			text += fmt.Sprintf(m.l("\n\nДо отката примерно %d сек.", "\n\nRollback in approximately %d seconds."), max(0, int(time.Until(m.nodeInstallJob.RollbackAt).Seconds())))
		}
		return text
	case "nodeInstallPending":
		rows := []string{title, ""}
		if len(m.nodeInstallJobs) == 0 {
			rows = append(rows, m.l("Незавершённых установок нет.", "No pending installations."))
		}
		for i, j := range m.nodeInstallJobs {
			line := fmt.Sprintf("%s · %s · %d/%d · %s", j.Params.Name, j.Params.Address, j.Stage, len(installer.NodeInstallStages()), j.ID[:8])
			if i == m.nodeCursor {
				line = accent.Render("› " + line)
			} else {
				line = "  " + line
			}
			rows = append(rows, line)
		}
		return strings.Join(rows, "\n")
	case "nodeInstallResult":
		text := title + "\n\n" + m.panelResult
		if m.nodeInstallJob != nil {
			text += "\n\n" + m.l("Установка: ", "Installation: ") + m.nodeInstallJob.ID
			if m.nodeInstallJob.NodeUUID != "" {
				text += "\nNode UUID: " + m.nodeInstallJob.NodeUUID
			}
		}
		return text + "\n\n" + m.l("Enter — продолжить / в меню · Esc — в меню", "Enter — resume / menu · Esc — menu")
	}
	return ""
}
