package ui

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/ConsolePro/RemnaManager/internal/installer"
	"github.com/ConsolePro/RemnaManager/internal/services"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
)

func (m Model) openRemoteInputs() (tea.Model, tea.Cmd) {
	remote := m.remoteDraft
	if remote.SSH.Port == 0 {
		remote.SSH.Port = 22
	}
	if remote.User == "" {
		remote.User = "root"
	}
	if remote.Proxy == "" {
		remote.Proxy = "caddy"
	}
	m.inputs = []textinput.Model{newInput("subscription-server.example.com", remote.SSH.Host), newInput("22", strconv.Itoa(remote.SSH.Port)), newInput("root", remote.User), newInput("/root/.ssh/id_ed25519", remote.SSH.IdentityFile), newInput("nginx / caddy", remote.Proxy)}
	m.focus = 0
	m.problem = ""
	m.screen = "panelRemoteInput"
	return m, m.inputs[0].Focus()
}

func (m Model) updateRemoteInput(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	if m.screen == "panelRemoteTrust" {
		if key == "enter" {
			if m.remoteInstallWizard {
				m.inputs = m.panelInstallInputs
				m.focus = 0
				m.panelInstallInputs = nil
				m.screen = "confirm"
				return m, nil
			}
			m.panelRequest = services.PanelRequest{Action: "sub-ssh-save", Remote: m.remoteDraft, Fingerprint: m.remoteFingerprint}
			m.screen = "panelConfirm"
		}
		return m, nil
	}
	if key == "tab" || key == "shift+tab" || key == "up" || key == "down" {
		m.inputs[m.focus].Blur()
		step := 1
		if key == "shift+tab" || key == "up" {
			step = len(m.inputs) - 1
		}
		m.focus = (m.focus + step) % len(m.inputs)
		return m, m.inputs[m.focus].Focus()
	}
	if key == "enter" {
		if m.focus < len(m.inputs)-1 {
			m.inputs[m.focus].Blur()
			m.focus++
			return m, m.inputs[m.focus].Focus()
		}
		port, err := strconv.Atoi(strings.TrimSpace(m.inputs[1].Value()))
		if err != nil {
			m.problem = m.l("Некорректный SSH-порт", "Invalid SSH port")
			return m, nil
		}
		remote := services.SubscriptionRemote{SSH: services.NodeSSH{Host: strings.TrimSpace(m.inputs[0].Value()), Port: port, IdentityFile: strings.TrimSpace(m.inputs[3].Value())}, User: strings.TrimSpace(m.inputs[2].Value()), Proxy: strings.ToLower(strings.TrimSpace(m.inputs[4].Value()))}
		if err = installer.ValidateSubscriptionRemote(remote); err != nil {
			m.problem = m.errorText(err)
			return m, nil
		}
		m.panelRequest = services.PanelRequest{Action: "sub-ssh-scan", Remote: remote}
		m.panelTitle = m.l("SSH-доступ к подписке", "Subscription SSH access")
		return m.startPanelOperation()
	}
	var cmd tea.Cmd
	m.inputs[m.focus], cmd = m.inputs[m.focus].Update(msg)
	return m, cmd
}

func (m Model) remoteInputView() string {
	if m.screen == "panelRemoteTrust" {
		return accent.Render(m.l("Подтвердите SSH-сервер", "Confirm SSH server")) + fmt.Sprintf("\n\n%s@%s:%d\n%s\n\n", m.remoteDraft.User, m.remoteDraft.SSH.Host, m.remoteDraft.SSH.Port, m.remoteFingerprint) + warning.Render(m.l("Сверьте fingerprint с консолью сервера.\nEnter — доверять этому ключу · Esc — отменить", "Compare this fingerprint with the server console.\nEnter — trust this key · Esc — cancel"))
	}
	rows := []string{accent.Render(m.l("Удалённая страница подписки", "Remote subscription page")), ""}
	labels := []string{m.l("SSH-сервер", "SSH host"), m.l("SSH-порт", "SSH port"), m.l("Пользователь (root или sudo без пароля)", "User (root or passwordless sudo)"), m.l("Путь к приватному ключу (пусто — ssh-agent)", "Private key path (empty for ssh-agent)"), m.l("Прокси удалённого сервера", "Remote server proxy")}
	for i, label := range labels {
		rows = append(rows, label+": "+m.inputs[i].View())
	}
	return strings.Join(rows, "\n") + "\n\n" + m.l("Будут установлены только подписка, Docker и выбранный прокси.\nEnter — дальше · Tab — поле · Esc — отменить", "Only subscription, Docker and the selected proxy will be installed.\nEnter — continue · Tab — next field · Esc — cancel")
}
