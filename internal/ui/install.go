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

var protectionChoices = []string{"cookie", "tinyauth", "mfa"}

func (m *Model) moveProtectionChoice(key string) {
	step := 1
	if key == "up" || key == "left" || key == "shift+tab" {
		step = len(protectionChoices) - 1
	}
	m.protectionChoice = (m.protectionChoice + step) % len(protectionChoices)
}

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
	m.installRemote = p.RemoteSubscription != nil
	m.certificateAuthority = p.CertificateAuthority
	if m.certificateAuthority == "" {
		m.certificateAuthority = "zerossl"
	}
	if p.RemoteSubscription != nil {
		m.remoteDraft = *p.RemoteSubscription
	}
	m.proxyChoice = 0
	if p.Proxy == "caddy" {
		m.proxyChoice = 1
	}
	m.protectionChoice = 0
	for i, mode := range protectionChoices {
		if p.Protection == mode {
			m.protectionChoice = i
		}
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
		p.CertificateAuthority = m.certificateAuthority
		p.Admin = strings.TrimSpace(m.inputs[2].Value())
		p.SSHPorts, _ = installer.ParsePorts(m.inputs[3].Value())
		p.Protection = protectionChoices[m.protectionChoice]
		if m.installRemote {
			remote := m.remoteDraft
			p.RemoteSubscription = &remote
		}
		if p.Protection == "tinyauth" && len(m.inputs) > 4 {
			p.AuthDomain = strings.ToLower(strings.TrimSpace(m.inputs[4].Value()))
		}
	}
	return p
}

func (m *Model) prepareProtectionInput() {
	if len(m.inputs) < 4 {
		return
	}
	if m.protectionChoice == 1 && len(m.inputs) == 4 {
		domain := ""
		if s, ok := m.liveInstaller(); ok {
			domain = s.Defaults().AuthDomain
		}
		m.inputs = append(m.inputs, newInput("auth.panel.example.com", domain))
	} else if m.protectionChoice != 1 && len(m.inputs) > 4 {
		m.inputs = m.inputs[:4]
	}
	for i := range m.inputs {
		m.inputs[i].Blur()
	}
	m.focus = 0
}

func (m Model) protectionOptionsView() string {
	lines := []string{accent.Render(m.l("Защита панели", "Panel protection")), ""}
	for i, name := range []string{m.l("Cookie-защита (по умолчанию)", "Cookie protection (default)"), m.l("Страница входа TinyAuth", "TinyAuth login page"), m.l("Портал входа с MFA", "Login portal with MFA")} {
		row := "  " + name
		if i == m.protectionChoice {
			row = accent.Render("› " + name)
		}
		lines = append(lines, row)
	}
	lines = append(lines, "", m.l("Все три режима работают с Nginx и Caddy.\nCookie: секретная ссылка; без допуска — 403.\nTinyAuth: отдельный вход; нужен auth.panel.example.com.\nMFA: портал /r на домене панели, обязательный второй фактор.\nПосле портала остаётся штатный вход Remnawave.", "All three modes work with Nginx and Caddy.\nCookie: a secret link; no admission means 403.\nTinyAuth: separate login; requires auth.panel.example.com.\nMFA: /r portal on the panel domain, mandatory second factor.\nThe Remnawave login remains after the portal."), "", m.l("↑/↓ или Tab — выбор · Enter — далее", "↑/↓ or Tab — select · Enter — continue"))
	return strings.Join(lines, "\n")
}
func (m Model) confirmFirewall() tea.Cmd {
	service, ok := m.liveInstaller()
	if !ok {
		return nil
	}
	return func() tea.Msg { return confirmedInstallMsg{service.Confirm(m.ctx)} }
}
func (m Model) installOptionsView() string {
	lines := []string{m.l("Обратный прокси", "Reverse proxy"), ""}
	for i, proxy := range []string{"Nginx Docker + acme.sh", m.l("Caddy Docker (автоматический HTTPS)", "Caddy Docker (automatic HTTPS)")} {
		row := "  " + proxy
		if i == m.proxyChoice {
			row = accent.Render("› " + proxy)
		}
		lines = append(lines, row)
	}
	if s, ok := m.liveInstaller(); ok && s.Defaults().PanelDomain != "" {
		lines = append(lines, "", m.l("Продолжение установки: сохраните прежние домены, прокси и логин.", "Resuming installation: keep the saved domains, proxy and login."))
	}
	layout := m.l("Один сервер", "One server")
	if m.installRemote {
		layout = m.l("Два сервера (подписка через SSH)", "Two servers (subscription over SSH)")
	}
	lines = append(lines, "", layout, m.l("S — изменить размещение подписки", "S — change subscription placement"))
	lines = append(lines, "ACME: "+m.certificateAuthority, m.l("A — Let's Encrypt / ZeroSSL (Nginx)", "A — Let's Encrypt / ZeroSSL (Nginx)"))
	return strings.Join(lines, "\n") + "\n\n" + m.l("↑/↓ или Tab — выбор · Enter — параметры", "↑/↓ or Tab — select · Enter — parameters")
}
func (m Model) realDomainsView() string {
	labels := [][2]string{{"Домен панели", "Panel domain"}, {"Домен подписки", "Subscription domain"}, {"Администратор", "Administrator"}, {"Разрешённые SSH-порты", "SSH ports to preserve"}}
	if m.protectionChoice == 1 {
		labels = append(labels, [2]string{"Домен TinyAuth (прямой поддомен панели)", "TinyAuth domain (direct child of panel domain)"})
	}
	lines := []string{accent.Render(m.l("Параметры установки", "Installation parameters")), ""}
	for i, label := range labels {
		lines = append(lines, m.l(label[0], label[1])+": "+m.inputs[i].View())
	}
	lines = append(lines, "", m.l("Пароль будет сгенерирован и сохранён с правами 0600.", "A random password will be saved with permissions 0600."), m.l("Tab — поле · Enter — далее · Esc — назад", "Tab — field · Enter — next · Esc — back"))
	return strings.Join(lines, "\n")
}
func (m Model) realConfirmationView() string {
	p := m.parameters()
	return accent.Render(m.l("РЕАЛЬНАЯ УСТАНОВКА", "REAL INSTALLATION")) + fmt.Sprintf("\n\nPanel: %s\nSubscription: %s\nProxy: %s\nProtection: %s\nTinyAuth domain: %s\nAdmin: %s\nACME email: %s\nSSH TCP: %v\n", p.PanelDomain, p.SubscriptionDomain, p.Proxy, p.Protection, p.AuthDomain, p.Admin, p.Email, p.SSHPorts) + "\n" + warning.Render(m.l("Будут установлены пакеты и контейнеры, настроены HTTPS и firewall.\nНовые входящие соединения: только SSH, TCP 80/443; Caddy также UDP 443.\nДругие входящие порты будут закрыты, существующие соединения сохранятся.", "Packages, containers, HTTPS and firewall will be configured.\nNew inbound connections: SSH, TCP 80/443; Caddy also UDP 443.\nOther inbound ports will be blocked; existing connections remain.")) + "\n\n" + m.l("Enter — начать установку · Esc — отменить", "Enter — begin installation · Esc — cancel")
}
