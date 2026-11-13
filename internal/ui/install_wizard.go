package ui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/ConsolePro/RemnaManager/internal/services"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
)

var certificateMethods = []string{"acme", "cloudflare", "gcore", "bunny", "custom"}
var certificateLabels = []string{"acme.sh", "Cloudflare DNS API (Wildcard)", "Gcore DNS API (Wildcard)", "Bunny.net DNS API (Wildcard)", "Свой сертификат / Own certificate"}

type installPreflightMsg struct {
	run  int
	text string
	err  error
}

func certificateLabel(p services.PanelParameters) string {
	if p.Proxy == "caddy" {
		return "Caddy HTTPS"
	}
	for i, method := range certificateMethods {
		if p.Certificate.Method == method {
			return certificateLabels[i]
		}
	}
	return certificateLabels[0]
}

func (m Model) choiceView(title string, choices []string, selected int, help string) string {
	rows := []string{accent.Render(title), ""}
	for i, name := range choices {
		row := fmt.Sprintf("  %d. %s", i+1, name)
		if i == selected {
			row = accent.Render("›" + row[1:])
		}
		rows = append(rows, row)
	}
	return strings.Join(rows, "\n") + "\n\n" + help + "\n\n" + m.l("↑/↓ — выбор · Enter — далее · Esc — назад", "↑/↓ — select · Enter — continue · Esc — back")
}

func (m Model) installWizardView() string {
	switch m.screen {
	case "installLayout":
		selected := 0
		if m.installRemote {
			selected = 1
		}
		return m.choiceView(m.l("Панель и страница подписки", "Panel and subscription page"), []string{m.l("На одном сервере", "One server"), m.l("На разных серверах", "Separate servers")}, selected, "")
	case "certificateOptions":
		return m.choiceView(m.l("Способ получения SSL-сертификата", "SSL certificate method"), certificateLabels, m.certificateChoice, m.l("acme.sh — получение сертификата без API-ключа.\nDNS-записи доменов необходимо настроить вручную.\n\nWildcard — автоматическая DNS-проверка через API\nвыбранного DNS-провайдера (TXT, не постоянные A/AAAA).", "acme.sh obtains certificates without an API key.\nConfigure domain DNS records manually.\n\nWildcard uses the selected provider API for DNS-01\nTXT validation; it does not create permanent A/AAAA records."))
	case "installPreflight":
		return m.l("Проверка DNS, SSH, портов и параметров SSL…\nEsc — вернуться", "Checking DNS, SSH, ports and SSL parameters…\nEsc — back")
	}
	rows := []string{accent.Render(m.l("Параметры защиты и SSL", "Protection and SSL parameters")), ""}
	labels := []string{m.l("API-токен DNS", "DNS API token")}
	if m.screen == "protectionDetails" {
		labels = []string{m.l("Домен TinyAuth", "TinyAuth domain")}
	} else if m.certificateChoice == 4 {
		labels = []string{m.l("Сертификат/fullchain", "Certificate/fullchain"), m.l("Приватный ключ", "Private key")}
		if m.installRemote {
			labels = append(labels, m.l("Сертификат подписки (локальный файл)", "Subscription certificate (local file)"), m.l("Ключ подписки (локальный файл)", "Subscription key (local file)"))
		}
	}
	for i, input := range m.inputs {
		rows = append(rows, labels[i]+": "+input.View())
	}
	return strings.Join(rows, "\n") + "\n\n" + m.l("Tab — поле · Enter — далее · Esc — назад", "Tab — field · Enter — continue · Esc — back")
}

func (m Model) startInstallPreflight() (tea.Model, tea.Cmd) {
	p := m.parameters()
	m.problem = ""
	m.run++
	run := m.run
	m.cancelInstall()
	m.installCtx, m.installCancel = context.WithCancel(m.ctx)
	ctx := m.installCtx
	m.screen = "installPreflight"
	return m, func() tea.Msg {
		ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		defer cancel()
		if checker, ok := m.service.(services.PanelPreflighter); ok {
			text, err := checker.Preflight(ctx, p)
			return installPreflightMsg{run, text, err}
		}
		// Demo/test installers have no host access; do not claim live checks.
		return installPreflightMsg{run, m.l("Предварительная проверка недоступна в демонстрации.", "Live preflight is unavailable in this preview."), nil}
	}
}

func (m Model) updateInstallWizard(msg tea.Msg) (tea.Model, tea.Cmd, bool) {
	if result, ok := msg.(installPreflightMsg); ok {
		if result.run == m.run && m.screen == "installPreflight" {
			if result.err != nil {
				m.screen = "protectionOptions"
				m.problem = m.errorText(result.err)
			} else {
				m.preflightSummary = result.text
				m.screen = "confirm"
			}
		}
		return m, nil, true
	}
	keyMsg, ok := msg.(tea.KeyMsg)
	if !ok || m.exitConfirm {
		return m, nil, false
	}
	key := keyMsg.String()
	switch m.screen {
	case "installLayout", "installOptions", "certificateOptions", "certificateInput", "protectionOptions", "protectionDetails":
	default:
		return m, nil, false
	}
	if key == "esc" || key == "ctrl+c" || key == "f1" {
		return m, nil, false
	}
	if m.screen == "certificateInput" || m.screen == "protectionDetails" {
		if key == "tab" || key == "shift+tab" || key == "up" || key == "down" {
			m.inputs[m.focus].Blur()
			step := 1
			if key == "up" || key == "shift+tab" {
				step = len(m.inputs) - 1
			}
			m.focus = (m.focus + step) % len(m.inputs)
			return m, m.inputs[m.focus].Focus(), true
		}
		if key == "enter" {
			if m.focus < len(m.inputs)-1 {
				m.inputs[m.focus].Blur()
				m.focus++
				return m, m.inputs[m.focus].Focus(), true
			}
			if m.screen == "protectionDetails" {
				value := strings.TrimSpace(m.inputs[0].Value())
				m.inputs = m.wizardInputs
				m.inputs = m.inputs[:4]
				m.inputs = append(m.inputs, newInput("auth.panel.example.com", value))
				next, cmd := m.startInstallPreflight()
				return next, cmd, true
			}
			if m.certificateChoice == 4 {
				m.certificateDraft.CertificateFile = strings.TrimSpace(m.inputs[0].Value())
				m.certificateDraft.KeyFile = strings.TrimSpace(m.inputs[1].Value())
				if m.installRemote {
					m.certificateDraft.RemoteCertificateFile = strings.TrimSpace(m.inputs[2].Value())
					m.certificateDraft.RemoteKeyFile = strings.TrimSpace(m.inputs[3].Value())
				}
			} else {
				m.certificateDraft.Token = m.inputs[0].Value()
			}
			m.inputs = m.wizardInputs
			if m.editingCertificates {
				m.panelRequest.Certificate = m.certificateDraft
				m.screen = "panelConfirm"
				return m, nil, true
			}
			m.screen = "protectionOptions"
			return m, nil, true
		}
		var cmd tea.Cmd
		m.inputs[m.focus], cmd = m.inputs[m.focus].Update(msg)
		return m, cmd, true
	}
	move := key == "up" || key == "down" || key == "left" || key == "right" || key == "tab" || key == "shift+tab"
	switch m.screen {
	case "installLayout":
		if move {
			m.installRemote = !m.installRemote
		}
		if key == "enter" {
			m.screen = "domains"
			m.focus = 0
			return m, m.inputs[0].Focus(), true
		}
	case "installOptions":
		if move {
			m.proxyChoice = 1 - m.proxyChoice
		}
		if key == "enter" {
			m.screen = "protectionOptions"
			if m.proxyChoice == 0 {
				m.screen = "certificateOptions"
			}
		}
	case "certificateOptions":
		if move {
			step := 1
			if key == "up" || key == "left" || key == "shift+tab" {
				step = 4
			}
			m.certificateChoice = (m.certificateChoice + step) % 5
		}
		if key == "enter" {
			m.certificateDraft.Method = certificateMethods[m.certificateChoice]
			if m.certificateChoice == 0 {
				if m.editingCertificates {
					m.panelRequest.Certificate = m.certificateDraft
					m.screen = "panelConfirm"
					return m, nil, true
				}
				m.screen = "protectionOptions"
				return m, nil, true
			}
			m.wizardInputs = m.inputs
			c := m.certificateDraft
			m.inputs = []textinput.Model{newInput("", c.Token)}
			m.inputs[0].EchoMode = textinput.EchoPassword
			if m.certificateChoice == 4 {
				m.inputs = []textinput.Model{newInput("/root/fullchain.pem", c.CertificateFile), newInput("/root/privkey.pem", c.KeyFile)}
				if m.installRemote {
					m.inputs = append(m.inputs, newInput("/root/sub-fullchain.pem", c.RemoteCertificateFile), newInput("/root/sub-key.pem", c.RemoteKeyFile))
				}
			}
			m.focus = 0
			m.screen = "certificateInput"
			return m, m.inputs[0].Focus(), true
		}
	case "protectionOptions":
		if move {
			m.moveProtectionChoice(key)
		}
		if key == "enter" {
			if m.protectionChoice == 1 {
				value := "auth." + m.inputs[0].Value()
				if len(m.inputs) > 4 {
					value = m.inputs[4].Value()
				}
				m.wizardInputs = m.inputs
				m.inputs = []textinput.Model{newInput("auth.panel.example.com", value)}
				m.focus = 0
				m.screen = "protectionDetails"
				return m, m.inputs[0].Focus(), true
			}
			if len(m.inputs) > 4 {
				m.inputs = m.inputs[:4]
			}
			next, cmd := m.startInstallPreflight()
			return next, cmd, true
		}
	}
	return m, nil, true
}
