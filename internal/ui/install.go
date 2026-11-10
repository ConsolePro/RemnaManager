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

type confirmedInstallMsg struct{ err error }

func (m Model) liveInstaller() (services.LivePanelInstaller, bool) {
	s, ok := m.service.(services.LivePanelInstaller)
	return s, ok
}
func (m Model) liveInstall() bool { _, ok := m.liveInstaller(); return ok }
func (m *Model) cancelInstall() {
	if m.installCancel != nil {
		m.installCancel()
		m.installCancel = nil
	}
}
func (m *Model) prepareInstallInputs() {
	p := services.PanelParameters{}
	if s, ok := m.liveInstaller(); ok {
		p = s.Defaults()
	}
	m.proxyChoice = 0
	if p.Proxy == "caddy" {
		m.proxyChoice = 1
	}
	m.inputs = []textinput.Model{newInput("panel.example.com", p.PanelDomain), newInput("sub.example.com", p.SubscriptionDomain)}
	if m.liveInstall() {
		ports := []string{}
		for _, port := range p.SSHPorts {
			ports = append(ports, strconv.Itoa(port))
		}
		m.inputs = append(m.inputs, newInput("admin", p.Admin), newInput("22", strings.Join(ports, ", ")))
	}
	m.focus = 0
}
func (m Model) installationParameters() services.PanelParameters {
	p := services.PanelParameters{PanelDomain: strings.ToLower(strings.TrimSpace(m.inputs[0].Value())), SubscriptionDomain: strings.ToLower(strings.TrimSpace(m.inputs[1].Value()))}
	if m.liveInstall() && len(m.inputs) >= 4 {
		p.Proxy = []string{"nginx", "caddy"}[m.proxyChoice]
		p.Email = m.cfg.Email
		p.Admin = strings.TrimSpace(m.inputs[2].Value())
		p.SSHPorts, _ = installer.ParsePorts(m.inputs[3].Value())
	}
	return p
}
func (m Model) confirmFirewall() tea.Cmd {
	service, ok := m.liveInstaller()
	if !ok {
		return nil
	}
	return func() tea.Msg { return confirmedInstallMsg{service.Confirm(m.ctx)} }
}
func (m Model) installOptionsView() string {
	proxy := []string{"Nginx + Certbot", "Caddy (automatic HTTPS)"}[m.proxyChoice]
	return m.l("Обратный прокси", "Reverse proxy") + "\n\n" + accent.Render("‹ "+proxy+" ›") + "\n\n" + m.l("←/→ — выбор · Enter — параметры", "←/→ — select · Enter — parameters")
}
func (m Model) realDomainsView() string {
	labels := [][2]string{{"Домен панели", "Panel domain"}, {"Домен подписки", "Subscription domain"}, {"Администратор", "Administrator"}, {"Разрешённые SSH-порты", "SSH ports to preserve"}}
	lines := []string{accent.Render(m.l("Параметры установки", "Installation parameters")), ""}
	for i, label := range labels {
		lines = append(lines, m.l(label[0], label[1])+": "+m.inputs[i].View())
	}
	lines = append(lines, "", m.l("Пароль будет сгенерирован и сохранён с правами 0600.", "A random password will be saved with permissions 0600."), m.l("Tab — поле · Enter — далее · Esc — назад", "Tab — field · Enter — next · Esc — back"))
	return strings.Join(lines, "\n")
}
func (m Model) realConfirmationView() string {
	p := m.parameters()
	return accent.Render(m.l("РЕАЛЬНАЯ УСТАНОВКА", "REAL INSTALLATION")) + fmt.Sprintf("\n\nPanel: %s\nSubscription: %s\nProxy: %s\nAdmin: %s\nACME email: %s\nSSH TCP: %v\n", p.PanelDomain, p.SubscriptionDomain, p.Proxy, p.Admin, p.Email, p.SSHPorts) + "\n" + warning.Render(m.l("Будут установлены пакеты и контейнеры, настроены HTTPS и firewall.\nНовые входящие соединения: только SSH, TCP 80/443; Caddy также UDP 443.\nДругие входящие порты будут закрыты, существующие соединения сохранятся.", "Packages, containers, HTTPS and firewall will be configured.\nNew inbound connections: SSH, TCP 80/443; Caddy also UDP 443.\nOther inbound ports will be blocked; existing connections remain.")) + "\n\n" + m.l("Enter — начать установку · Esc — отменить", "Enter — begin installation · Esc — cancel")
}
