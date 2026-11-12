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

func (m *Model) prepareNodeConnection() {
	p := m.nodeInstallParams
	m.inputs = []textinput.Model{newInput("203.0.113.10 / node.example.com", p.Address), newInput("22", strconv.Itoa(p.SSH.Port)), newInput(m.l("Пусто — пароль root", "Empty — root password"), p.SSH.IdentityFile)}
	m.focus = 0
	for i := range m.inputs {
		m.inputs[i].Width = max(1, min(60, m.width-12))
	}
}

func (m *Model) prepareNodeDetails() {
	p := m.nodeInstallParams
	if m.nodeInstallJob != nil && m.nodeInstallJob.DetailsPending && p.Name == "New_node" {
		p.Name = ""
		p.Domain = ""
	}
	if p.ProfileName == "" && (m.nodeInstallJob == nil || m.nodeInstallJob.Version < 4) {
		p.ProfileName = "Node_" + m.nodeInstallJob.ID[:8]
	}
	if p.APIPort == 0 {
		p.APIPort = 2222
	}
	if p.CountryCode == "" {
		p.CountryCode = "XX"
	}
	if p.SiteTemplate == "" {
		p.SiteTemplate = "random"
	}
	m.inputs = []textinput.Model{newInput("Node-01", p.Name), newInput("XX", p.CountryCode), newInput("FN1001", p.ProfileName), newInput("2222", strconv.Itoa(p.APIPort)), newInput("cover.example.com", p.Domain), newInput("random", p.SiteTemplate), newInput(m.l("Пусто — определить по SSH", "Empty — detect over SSH"), p.PanelIP), newInput(m.l("Пусто — название профиля", "Empty — profile name"), p.HostName)}
	m.focus = 0
	for i := range m.inputs {
		m.inputs[i].Width = max(1, min(60, m.width-12))
	}
}
func (m *Model) restoreNodeDetails(focus int) {
	m.inputs = m.nodeDetailsInputs
	m.nodeDetailsInputs = nil
	m.focus = focus
	m.screen = "nodeDetailsInput"
	if len(m.inputs) > focus {
		m.inputs[focus].Focus()
	}
}

func (m Model) updateNodeWizard(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	if m.screen == "nodeCountry" {
		list := installer.FilterNodeCountries(m.inputs[0].Value(), m.cfg.Language)
		if key == "up" {
			m.nodeCountryCursor = max(0, m.nodeCountryCursor-1)
		} else if key == "down" {
			m.nodeCountryCursor = min(max(0, len(list)-1), m.nodeCountryCursor+1)
		} else if key == "enter" && len(list) > 0 {
			country := list[min(m.nodeCountryCursor, len(list)-1)].Code
			m.restoreNodeDetails(1)
			m.inputs[1].SetValue(country)
			m.inputs[1].Blur()
			m.focus = 2
			if m.nodeInstallJob != nil && m.nodeInstallJob.Version >= 4 {
				m.focus = 3
			}
			return m, m.inputs[m.focus].Focus()
		} else {
			var cmd tea.Cmd
			m.inputs[0], cmd = m.inputs[0].Update(msg)
			m.nodeCountryCursor = 0
			return m, cmd
		}
		return m, nil
	}
	if m.screen == "nodeSite" {
		choices := append([]string{"random"}, installer.NodeSiteTemplates...)
		if key == "up" || key == "left" {
			m.nodeSiteCursor = (m.nodeSiteCursor + len(choices) - 1) % len(choices)
		}
		if key == "down" || key == "right" || key == "tab" {
			m.nodeSiteCursor = (m.nodeSiteCursor + 1) % len(choices)
		}
		if key == "enter" {
			choice := choices[m.nodeSiteCursor]
			m.restoreNodeDetails(5)
			m.inputs[5].SetValue(choice)
			m.inputs[5].Blur()
			m.focus = 6
			return m, m.inputs[6].Focus()
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
		if m.nodeInstallJob != nil && m.nodeInstallJob.Version >= 4 && m.focus == 2 {
			m.focus = (m.focus + step) % len(m.inputs)
		}
		return m, m.inputs[m.focus].Focus()
	}
	if key == "enter" {
		if m.screen == "nodeDetailsInput" && (m.focus == 1 || m.focus == 5) {
			m.inputs[m.focus].Blur()
			m.nodeDetailsInputs = m.inputs
			m.scroll = 0
			if m.focus == 1 {
				m.screen = "nodeCountry"
				m.nodeCountryCursor = 0
				m.inputs = []textinput.Model{newInput(m.l("Страна или код, например Германия / DE", "Country or code, e.g. Germany / DE"), "")}
				m.focus = 0
				return m, m.inputs[0].Focus()
			}
			m.screen = "nodeSite"
			m.nodeSiteCursor = 0
			return m, nil
		}
		if m.focus < len(m.inputs)-1 {
			m.inputs[m.focus].Blur()
			m.focus++
			if m.screen == "nodeDetailsInput" && m.nodeInstallJob != nil && m.nodeInstallJob.Version >= 4 && m.focus == 2 {
				m.focus++
			}
			return m, m.inputs[m.focus].Focus()
		}
		if m.screen == "nodeConnectInput" {
			port, err := strconv.Atoi(strings.TrimSpace(m.inputs[1].Value()))
			if err != nil {
				m.problem = m.l("Неверный SSH-порт.", "Invalid SSH port.")
				return m, nil
			}
			ssh := services.NodeSSH{Host: strings.ToLower(strings.TrimSpace(m.inputs[0].Value())), Port: port, IdentityFile: strings.TrimSpace(m.inputs[2].Value())}
			if err := installer.ValidateNodeSSH(ssh); err != nil {
				m.problem = m.errorText(err)
				return m, nil
			}
			m.nodeInstallParams = services.NodeInstallParameters{Address: ssh.Host, SSH: ssh}
			return m.startNodeRequest(services.NodeRequest{Action: "node-install-connect-scan", Install: m.nodeInstallParams})
		}
		port, err := strconv.Atoi(strings.TrimSpace(m.inputs[3].Value()))
		if err != nil {
			m.problem = m.l("Неверный порт API ноды.", "Invalid node API port.")
			return m, nil
		}
		p := m.nodeInstallParams
		p.Name, p.CountryCode, p.ProfileName = strings.TrimSpace(m.inputs[0].Value()), strings.ToUpper(strings.TrimSpace(m.inputs[1].Value())), strings.TrimSpace(m.inputs[2].Value())
		p.APIPort, p.Domain, p.SiteTemplate, p.PanelIP = port, strings.ToLower(strings.TrimSpace(m.inputs[4].Value())), strings.ToLower(strings.TrimSpace(m.inputs[5].Value())), strings.TrimSpace(m.inputs[6].Value())
		p.HostName = strings.TrimSpace(m.inputs[7].Value())
		p.HostLanguage = m.cfg.Language
		if m.nodeInstallJob != nil && m.nodeInstallJob.Version >= 4 {
			p.ProfileName = p.Name
		}
		if err := installer.ValidateNodeInstall(p); err != nil {
			m.problem = m.errorText(err)
			return m, nil
		}
		if m.nodeInstallJob == nil || m.nodeInstallJob.Version >= 3 {
			if err := installer.ValidateNodeShortProfileName(p); err != nil {
				m.problem = m.errorText(err)
				return m, nil
			}
		}
		m.nodeInstallParams = p
		m.problem = ""
		m.screen = "nodeInstallConfirm"
		return m, nil
	}
	var cmd tea.Cmd
	m.inputs[m.focus], cmd = m.inputs[m.focus].Update(msg)
	return m, cmd
}

func (m Model) nodeWizardView() string {
	title := accent.Render(m.nodeTitle)
	if m.screen == "nodeCountry" {
		rows := []string{title, "", m.l("Выберите страну (список Remnawave)", "Choose country (Remnawave list)"), m.inputs[0].View(), ""}
		list := installer.FilterNodeCountries(m.inputs[0].Value(), m.cfg.Language)
		start := max(0, min(m.nodeCountryCursor-2, len(list)-6))
		for i := start; i < min(len(list), start+6); i++ {
			label := "  " + list[i].Label(m.cfg.Language)
			if i == m.nodeCountryCursor {
				label = accent.Render("› " + list[i].Label(m.cfg.Language))
			}
			rows = append(rows, label)
		}
		if len(list) == 0 {
			rows = append(rows, m.l("Страна не найдена.", "No matching country."))
		}
		return strings.Join(append(rows, "", m.l("Пишите для поиска · ↑/↓ — выбор · Enter — выбрать · Esc — назад", "Type to search · ↑/↓ — navigate · Enter — select · Esc — back")), "\n")
	}
	if m.screen == "nodeSite" {
		rows := []string{title, "", m.l("Готовый сайт selfsteal", "Prepared selfsteal website"), ""}
		choices := []string{m.l("Случайный из готовых шаблонов", "Random prepared template"), "Northline Studio — design", "Field Atlas — landscapes", "Common Table — journal"}
		for i, choice := range choices {
			prefix := "  "
			if i == m.nodeSiteCursor {
				prefix = "› "
			}
			rows = append(rows, prefix+choice)
		}
		return strings.Join(append(rows, "", m.l("Сайт уже подготовлен: страницы, стили и иконка.\nДомен для HTTPS должен принадлежать вам и вести на ноду.\n↑/↓ — выбор · Enter — выбрать", "Pages, styles and icon are prepared in advance.\nThe HTTPS domain must be yours and point to the node.\n↑/↓ — select · Enter — continue")), "\n")
	}
	if m.screen == "nodeConnectInput" {
		rows := []string{title, "", m.l("Шаг 1. Подключение к серверу от root", "Step 1. Connect to the server as root"), ""}
		labels := [][2]string{{"IP или домен сервера (DNS должен вести на ноду)", "Server IP or domain (DNS must point to this node)"}, {"SSH-порт", "SSH port"}, {"Ваш SSH-ключ (необязательно; пусто — пароль root)", "Your SSH key (optional; empty — root password)"}}
		for i, label := range labels {
			rows = append(rows, m.l(label[0], label[1]), m.inputs[i].View())
		}
		return strings.Join(append(rows, "", m.l("Далее проверим SSH-сервер и запросим пароль root.\nПароль вводится скрыто в OpenSSH и не сохраняется.\nМенеджер создаст свой ключ и проверит новый вход по нему.\nКлюч от панели хостинга не нужен.", "Next: verify the SSH server, then enter the root password.\nOpenSSH hides password input; the password is not saved.\nThe manager creates its own key and verifies a fresh key-only login.\nNo hosting-provider private key is needed.")), "\n")
	}
	rows := []string{title, "", fmt.Sprintf("root@%s · SSH %d", m.nodeInstallParams.Address, m.nodeInstallParams.SSH.Port), ""}
	profileLabel := m.l("Короткое имя профиля (FN1001 → VLESS_REALITY_FN1001)", "Short profile name (FN1001 → VLESS_REALITY_FN1001)")
	hostLabel := m.l("Название хоста подписки (пусто — название профиля)", "Subscription host name (empty — profile name)")
	if m.nodeInstallJob != nil && m.nodeInstallJob.Version < 3 {
		profileLabel = m.l("Название профиля (латиница; tag: VLESS_REALITY_…)", "Profile name (Latin characters; tag: VLESS_REALITY_…)")
		hostLabel = m.l("Название хоста подписки (пусто — название ноды)", "Subscription host name (empty — node name)")
	}
	labels := [][2]string{{"Название ноды", "Node name"}, {"Страна (Enter — список с поиском)", "Country (Enter — searchable list)"}, {profileLabel, profileLabel}, {"Порт API ноды (доступ только с IP панели)", "Node API port (panel IP only)"}, {"Домен сайта и сертификата (A/AAAA на ноду)", "Website and certificate domain (A/AAAA to node)"}, {"Шаблон сайта (Enter — выбор)", "Website template (Enter — choose)"}, {"IP панели (пусто — определить по SSH)", "Panel IP (empty — detect over SSH)"}, {hostLabel, hostLabel}}
	for i, label := range labels {
		if i == 2 && m.nodeInstallJob != nil && m.nodeInstallJob.Version >= 4 {
			continue
		}
		rows = append(rows, m.l(label[0], label[1]), m.inputs[i].View())
	}
	return strings.Join(append(rows, "", m.l("Шаг 2. SSH-ключ настроен. Теперь параметры профиля и ноды.\nTab — поле · Enter — далее / список · Esc — сохранить и выйти", "Step 2. SSH key configured. Set profile and node parameters.\nTab — field · Enter — continue / list · Esc — save connection and leave")), "\n")
}
