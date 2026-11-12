package ui

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ConsolePro/RemnaManager/internal/routing"
	"github.com/ConsolePro/RemnaManager/internal/services"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
)

type routingUI struct {
	Quick                                    bool
	Inventory                                services.RoutingResult
	Spec                                     services.RoutingSpec
	Scheme                                   *services.RoutingScheme
	Cursor, Pool, Rule, Exit                 int
	Selection                                map[string]bool
	TargetContext, ExitKind, Operation, Text string
	Request                                  services.RoutingRequest
}
type routingMsg struct {
	run    int
	action string
	result services.RoutingResult
	err    error
}

func (m Model) openRouting(action string) (tea.Model, tea.Cmd) {
	if _, ok := m.service.(services.RoutingManager); !ok {
		m.screen, m.notice = "notice", m.l("Управление схемами недоступно в деморежиме.", "Routing schemes are unavailable in demo mode.")
		return m, nil
	}
	m.routes = routingUI{Operation: action}
	if action == "route-create" {
		m.routes.Quick = true
		return m.startRouting(services.RoutingRequest{Action: "route-quick-inventory"})
	}
	return m.startRouting(services.RoutingRequest{Action: "route-list"})
}

func (m Model) startRouting(request services.RoutingRequest) (tea.Model, tea.Cmd) {
	service, ok := m.service.(services.RoutingManager)
	if !ok {
		return m, nil
	}
	// Requests remain immutable even if an old command is cancelled and the
	// operator starts editing another scheme before its response arrives.
	data, _ := json.Marshal(request)
	var frozen services.RoutingRequest
	_ = json.Unmarshal(data, &frozen)
	m.cancelInstall()
	m.run++
	m.installCtx, m.installCancel = context.WithCancel(m.ctx)
	m.routes.Request = request
	m.screen = "routeProgress"
	m.problem, m.scroll = "", 0
	run, ctx := m.run, m.installCtx
	return m, func() tea.Msg {
		operation, cancel := context.WithTimeout(ctx, 90*time.Second)
		defer cancel()
		result, err := service.ManageRouting(operation, frozen)
		return routingMsg{run, frozen.Action, result, err}
	}
}

func (m Model) receiveRouting(msg routingMsg) (tea.Model, tea.Cmd) {
	if msg.run != m.run || m.screen != "routeProgress" {
		return m, nil
	}
	m.cancelInstall()
	if msg.result.Scheme != nil {
		m.routes.Scheme = msg.result.Scheme
	}
	if msg.err != nil {
		m.routes.Text = ""
		m.screen, m.problem = "routeResult", m.errorText(msg.err)
		if msg.action == "route-preview" {
			m.screen, m.routes.Cursor = "routeEditor", 5
		}
		if msg.action == "route-quick-preview" {
			m.screen = "routeQuickNodes"
		}
		return m, nil
	}
	m.routes.Cursor = 0
	switch msg.action {
	case "route-inventory", "route-quick-inventory":
		m.routes.Inventory = msg.result
		m.screen = "routeTemplates"
	case "route-list":
		m.routes.Inventory = msg.result
		m.routes.Operation = "route-list"
		m.screen = "routeList"
	case "route-preview", "route-quick-preview":
		m.screen = "routeConfirm"
		m.routes.Text = msg.result.Text
		if m.cfg.Language != "en" {
			m.routes.Text = msg.result.TextRU
		}
		m.routes.Request = services.RoutingRequest{Action: "route-apply", ID: msg.result.Scheme.ID}
		if m.routes.Quick {
			m.routes.Request.Action = "route-quick-apply"
		}
	default:
		m.screen = "routeResult"
		m.routes.Text = msg.result.Text
		if m.cfg.Language != "en" {
			m.routes.Text = msg.result.TextRU
		}
	}
	return m, nil
}

func csvValues(value string) []string {
	result := []string{}
	seen := map[string]bool{}
	for _, part := range strings.FieldsFunc(value, func(r rune) bool { return r == ',' || r == '\n' || r == ';' }) {
		part = strings.TrimSpace(part)
		if part != "" && !seen[part] {
			result = append(result, part)
			seen[part] = true
		}
	}
	return result
}

func domainValues(value string) []string {
	var result []string
	for _, part := range strings.FieldsFunc(value, func(r rune) bool { return r == ';' || r == '\n' }) {
		if part = strings.TrimSpace(part); part != "" {
			result = append(result, part)
		}
	}
	return result
}

func (m *Model) routeInputs(values, placeholders []string, screen string) tea.Cmd {
	m.inputs = []textinput.Model{}
	for i, value := range values {
		input := newInput(placeholders[i], value)
		input.CharLimit = 8192
		input.Width = max(1, min(70, m.width-12))
		m.inputs = append(m.inputs, input)
	}
	m.focus = 0
	m.screen = screen
	m.problem = ""
	m.scroll = 0
	return m.inputs[0].Focus()
}

func (m *Model) routeOpenHosts(index int) {
	m.routes.Pool = index
	m.routes.Cursor = 0
	m.routes.Selection = map[string]bool{}
	for _, id := range m.routes.Spec.Pools[index].Hosts {
		m.routes.Selection[id] = true
	}
	m.screen = "routeHosts"
	m.scroll = 0
}

func (m Model) routeTargets() []string {
	values := []string{"direct", "block"}
	for _, pool := range m.routes.Spec.Pools {
		values = append(values, pool.Name)
	}
	for _, exit := range m.routes.Spec.Exits {
		values = append(values, exit.Name)
	}
	return values
}

func (m Model) routeItemCount() int {
	switch m.screen {
	case "routeTemplates":
		return len(m.routes.Inventory.Templates)
	case "routeHosts":
		return len(m.routes.Inventory.Hosts)
	case "routeQuickNodes":
		return len(m.routes.Inventory.Nodes)
	case "routeEditor":
		return 6
	case "routePools":
		return len(m.routes.Spec.Pools) + 1
	case "routeRules":
		return len(m.routes.Spec.Rules) + 1
	case "routeExits":
		return len(m.routes.Spec.Exits) + 2
	case "routeTargets":
		return len(m.routeTargets())
	case "routeList":
		return len(m.routes.Inventory.Schemes)
	case "routeDetails":
		return 2
	}
	return 0
}

func (m Model) updateRouting(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	m.problem = ""
	if strings.HasSuffix(m.screen, "Input") {
		if key == "tab" || key == "shift+tab" || key == "up" || key == "down" {
			m.inputs[m.focus].Blur()
			delta := 1
			if key == "up" || key == "shift+tab" {
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
			switch m.screen {
			case "routeNameInput":
				m.routes.Spec.Name = strings.TrimSpace(m.inputs[0].Value())
				m.screen = "routeEditor"
			case "routePoolInput":
				name := strings.TrimSpace(m.inputs[0].Value())
				for _, target := range m.routeTargets() {
					if target == name {
						m.problem = m.l("Это имя уже занято.", "This name is already used.")
						return m, nil
					}
				}
				if !routing.ValidGroupName(name) {
					m.problem = m.l("Имя группы: строчные латинские буквы и цифры, максимум 12 символов.", "Group name: lowercase Latin letters and digits, at most 12 characters.")
					return m, nil
				}
				m.routes.Spec.Pools = append(m.routes.Spec.Pools, services.RoutingPool{Name: name})
				m.routeOpenHosts(len(m.routes.Spec.Pools) - 1)
			case "routeRuleInput":
				m.routes.TargetContext = "rule"
				m.routes.Cursor = 0
				m.screen = "routeTargets"
			case "routeExitInput":
				path := strings.TrimSpace(m.inputs[1].Value())
				data, err := readRoutingSecret(path)
				if err != nil {
					m.problem = m.errorText(err)
					return m, nil
				}
				exit := services.RoutingExit{Name: strings.TrimSpace(m.inputs[0].Value()), Kind: m.routes.ExitKind}
				if exit.Kind == "wireguard" {
					exit.WireGuard, err = routing.ParseWireGuard(data)
				} else {
					exit.URL = strings.TrimSpace(string(data))
					exit.ViaPool = strings.TrimSpace(m.inputs[2].Value())
				}
				if err != nil {
					m.problem = m.errorText(err)
					return m, nil
				}
				copy := m.routes.Spec
				copy.Exits = append(append([]services.RoutingExit(nil), copy.Exits...), exit)
				if err := routing.Validate(copy); err != nil {
					m.problem = m.errorText(err)
					return m, nil
				}
				m.routes.Spec = copy
				m.screen = "routeExits"
				m.routes.Cursor = 0
			}
			return m, nil
		}
		var cmd tea.Cmd
		m.inputs[m.focus], cmd = m.inputs[m.focus].Update(msg)
		return m, cmd
	}
	count := m.routeItemCount()
	if count > 0 {
		if key == "up" || key == "shift+tab" {
			m.routes.Cursor = (m.routes.Cursor + count - 1) % count
			return m, nil
		}
		if key == "down" || key == "tab" {
			m.routes.Cursor = (m.routes.Cursor + 1) % count
			return m, nil
		}
	}
	if key == " " && m.screen == "routeHosts" && count > 0 {
		host := m.routes.Inventory.Hosts[m.routes.Cursor]
		if !host.Disabled {
			m.routes.Selection[host.UUID] = !m.routes.Selection[host.UUID]
		}
		return m, nil
	}
	if key == " " && m.screen == "routeQuickNodes" && count > 0 {
		node := m.routes.Inventory.Nodes[m.routes.Cursor]
		if !node.IsDisabled {
			m.routes.Selection[node.UUID] = !m.routes.Selection[node.UUID]
		}
		return m, nil
	}
	if m.screen == "routeRules" && (key == "ctrl+up" || key == "ctrl+down") && m.routes.Cursor < len(m.routes.Spec.Rules) {
		i, j := m.routes.Cursor, m.routes.Cursor-1
		if key == "ctrl+down" {
			j = i + 1
		}
		if j >= 0 && j < len(m.routes.Spec.Rules) {
			m.routes.Spec.Rules[i], m.routes.Spec.Rules[j] = m.routes.Spec.Rules[j], m.routes.Spec.Rules[i]
			m.routes.Cursor = j
		}
		return m, nil
	}
	if key == "delete" {
		i := m.routes.Cursor
		switch m.screen {
		case "routeRules":
			if i < len(m.routes.Spec.Rules) {
				m.routes.Spec.Rules = append(m.routes.Spec.Rules[:i], m.routes.Spec.Rules[i+1:]...)
			}
		case "routePools":
			if i < len(m.routes.Spec.Pools) {
				name := m.routes.Spec.Pools[i].Name
				if m.routeTargetUsed(name) {
					m.problem = m.l("Выход используется правилом или маршрутом по умолчанию.", "This outbound is used by a rule or the default route.")
					return m, nil
				}
				m.routes.Spec.Pools = append(m.routes.Spec.Pools[:i], m.routes.Spec.Pools[i+1:]...)
			}
		case "routeExits":
			if i < len(m.routes.Spec.Exits) {
				name := m.routes.Spec.Exits[i].Name
				if m.routeTargetUsed(name) {
					m.problem = m.l("Выход используется правилом или маршрутом по умолчанию.", "This outbound is used by a rule or the default route.")
					return m, nil
				}
				m.routes.Spec.Exits = append(m.routes.Spec.Exits[:i], m.routes.Spec.Exits[i+1:]...)
			}
		}
		m.routes.Cursor = min(i, max(0, m.routeItemCount()-1))
		return m, nil
	}
	if key != "enter" && key != "right" {
		return m, nil
	}
	switch m.screen {
	case "routeTemplates":
		if count == 0 {
			return m, nil
		}
		if m.routes.Quick {
			m.routes.Spec = services.RoutingSpec{BaseTemplateUUID: m.routes.Inventory.Templates[m.routes.Cursor].UUID}
			m.routes.Selection = map[string]bool{}
			m.routes.Cursor, m.screen = 0, "routeQuickNodes"
			return m, nil
		}
		m.routes.Spec = services.RoutingSpec{Name: "Client_route", BaseTemplateUUID: m.routes.Inventory.Templates[m.routes.Cursor].UUID, DefaultTarget: "main", Pools: []services.RoutingPool{{Name: "main"}}, Rules: []services.RoutingRule{{Target: "direct", Domains: []string{"domain:ru", "domain:su", "domain:xn--p1ai", "geosite:category-ru"}, IPs: []string{"geoip:ru"}}}}
		m.routeOpenHosts(0)
	case "routeQuickNodes":
		ids := []string{}
		for _, node := range m.routes.Inventory.Nodes {
			if m.routes.Selection[node.UUID] && !node.IsDisabled {
				ids = append(ids, node.UUID)
			}
		}
		if len(ids) == 0 {
			m.problem = m.l("Отметьте ноды пробелом, затем нажмите Enter.", "Select nodes with Space, then press Enter.")
			return m, nil
		}
		return m.startRouting(services.RoutingRequest{Action: "route-quick-preview", Spec: m.routes.Spec, Nodes: ids})
	case "routeHosts":
		selected := []string{}
		for _, host := range m.routes.Inventory.Hosts {
			if m.routes.Selection[host.UUID] && !host.Disabled {
				selected = append(selected, host.UUID)
			}
		}
		if len(selected) == 0 {
			m.problem = m.l("Выберите хотя бы одно подключение пробелом.", "Select at least one host with Space.")
			return m, nil
		}
		m.routes.Spec.Pools[m.routes.Pool].Hosts = selected
		m.screen = "routeEditor"
		m.routes.Cursor = 1
		m.problem = ""
	case "routeEditor":
		switch m.routes.Cursor {
		case 0:
			return m.showRouteInputs([]string{m.routes.Spec.Name}, []string{"Client_route"}, "routeNameInput")
		case 1:
			m.screen = "routePools"
		case 2:
			m.screen = "routeExits"
		case 3:
			m.screen = "routeRules"
		case 4:
			m.screen = "routeTargets"
			m.routes.TargetContext = "default"
		case 5:
			return m.startRouting(services.RoutingRequest{Action: "route-preview", Spec: m.routes.Spec})
		}
		m.routes.Cursor = 0
	case "routePools":
		if m.routes.Cursor == len(m.routes.Spec.Pools) {
			return m.showRouteInputs([]string{fmt.Sprintf("pool%d", len(m.routes.Spec.Pools)+1)}, []string{"backup"}, "routePoolInput")
		}
		m.routeOpenHosts(m.routes.Cursor)
	case "routeRules":
		index := m.routes.Cursor
		m.routes.Rule = index
		rule := services.RoutingRule{}
		if index < len(m.routes.Spec.Rules) {
			rule = m.routes.Spec.Rules[index]
		}
		return m.showRouteInputs([]string{strings.Join(rule.Domains, ";"), strings.Join(rule.IPs, ","), strings.Join(rule.Protocols, ","), rule.Network, rule.Port}, []string{"domain:example.com;geosite:category-ru", "geoip:ru,192.0.2.0/24", "http,tls,quic,bittorrent", "tcp,udp", "443,8000-8100"}, "routeRuleInput")
	case "routeTargets":
		target := m.routeTargets()[m.routes.Cursor]
		if m.routes.TargetContext == "default" {
			m.routes.Spec.DefaultTarget = target
			m.screen = "routeEditor"
			m.routes.Cursor = 4
			return m, nil
		}
		rule := services.RoutingRule{Target: target, Domains: domainValues(m.inputs[0].Value()), IPs: csvValues(m.inputs[1].Value()), Protocols: csvValues(m.inputs[2].Value()), Network: strings.TrimSpace(m.inputs[3].Value()), Port: strings.TrimSpace(m.inputs[4].Value())}
		copy := m.routes.Spec
		copy.Rules = append([]services.RoutingRule(nil), copy.Rules...)
		if m.routes.Rule < len(copy.Rules) {
			copy.Rules[m.routes.Rule] = rule
		} else {
			copy.Rules = append(copy.Rules, rule)
		}
		if err := routing.Validate(copy); err != nil {
			m.problem = m.errorText(err)
			m.screen = "routeRuleInput"
			m.focus = 0
			return m, m.inputs[0].Focus()
		}
		m.routes.Spec = copy
		m.screen = "routeRules"
		m.routes.Cursor = m.routes.Rule
	case "routeExits":
		if m.routes.Cursor < len(m.routes.Spec.Exits) {
			m.problem = m.l("Для замены удалите неиспользуемый выход и добавьте новый. Секреты на экран не выводятся.", "To replace an unused outbound, delete it and add another. Secrets are never displayed.")
			return m, nil
		}
		m.routes.ExitKind = "wireguard"
		name := "warp"
		values := []string{name, ""}
		placeholders := []string{"warp", "/root/wgcf-profile.conf"}
		if m.routes.Cursor > len(m.routes.Spec.Exits) {
			m.routes.ExitKind = "vless"
			values = []string{"bridge", "", "main"}
			placeholders = []string{"bridge", "/root/bridge-vless.txt", "main (1 connection)"}
		}
		return m.showRouteInputs(values, placeholders, "routeExitInput")
	case "routeConfirm":
		return m.startRouting(m.routes.Request)
	case "routeList":
		if count == 0 {
			return m, nil
		}
		value := m.routes.Inventory.Schemes[m.routes.Cursor]
		m.routes.Scheme = &value
		m.screen = "routeDetails"
		m.routes.Cursor = 0
	case "routeDetails":
		if m.routes.Scheme == nil {
			return m, nil
		}
		action := "route-apply"
		if m.routes.Cursor == 1 {
			action = "route-delete"
		}
		m.routes.Request = services.RoutingRequest{Action: action, ID: m.routes.Scheme.ID}
		m.screen = "routeConfirm"
		m.routes.Text = m.l("Продолжить выбранную операцию с управляемым шаблоном? Подключения и сквады сохраняются.", "Continue this operation on the managed template? Hosts and squads are retained.")
	case "routeResult":
		if m.routes.Quick {
			m.screen = "menu"
			return m, nil
		}
		return m.startRouting(services.RoutingRequest{Action: "route-list"})
	}
	return m, nil
}

func (m Model) routeTargetUsed(name string) bool {
	if m.routes.Spec.DefaultTarget == name {
		return true
	}
	for _, rule := range m.routes.Spec.Rules {
		if rule.Target == name {
			return true
		}
	}
	for _, exit := range m.routes.Spec.Exits {
		if exit.ViaPool == name {
			return true
		}
	}
	return false
}

func readRoutingSecret(path string) ([]byte, error) {
	fail := func() ([]byte, error) {
		return nil, services.LocalizedError("Укажите абсолютный путь к обычному файлу до 64 КиБ на сервере менеджера. Ссылки и специальные файлы не поддерживаются.", "Use an absolute path to a regular file up to 64 KiB on the manager server. Symlinks and special files are refused.")
	}
	if !filepath.IsAbs(path) {
		return fail()
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > 64<<10 {
		return fail()
	}
	file, err := os.Open(path)
	if err != nil {
		return fail()
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, (64<<10)+1))
	if err != nil || len(data) > 64<<10 {
		return fail()
	}
	return data, nil
}

func (m *Model) backRouting() {
	m.cancelInstall()
	m.run++
	m.problem, m.scroll = "", 0
	m.routes.Cursor = 0
	switch m.screen {
	case "routeTemplates", "routeList", "routeEditor", "routeResult":
		m.screen = "menu"
	case "routeProgress":
		if m.routes.Quick && m.routes.Spec.BaseTemplateUUID != "" {
			m.screen = "routeQuickNodes"
		} else if m.routes.Operation == "route-list" && len(m.routes.Inventory.Schemes) > 0 {
			m.screen = "routeList"
		} else if m.routes.Spec.BaseTemplateUUID != "" {
			m.screen = "routeEditor"
		} else {
			m.screen = "menu"
		}
	case "routeRuleInput":
		m.screen = "routeRules"
	case "routeTargets":
		if m.routes.TargetContext == "rule" {
			m.screen = "routeRuleInput"
		} else {
			m.screen = "routeEditor"
		}
	case "routePoolInput":
		m.screen = "routePools"
	case "routeExitInput":
		m.screen = "routeExits"
	case "routeHosts":
		pool := m.routes.Pool
		if len(m.routes.Spec.Pools[pool].Hosts) == 0 {
			if pool == 0 {
				m.screen = "routeTemplates"
			} else {
				m.routes.Spec.Pools = append(m.routes.Spec.Pools[:pool], m.routes.Spec.Pools[pool+1:]...)
				m.screen = "routePools"
			}
		} else {
			m.screen = "routePools"
		}
	case "routeDetails":
		m.screen = "routeList"
	case "routeConfirm":
		if m.routes.Quick {
			m.screen = "routeQuickNodes"
		} else if m.routes.Operation == "route-list" {
			m.screen = "routeDetails"
		} else {
			m.screen = "routeEditor"
		}
	case "routeQuickNodes":
		m.screen = "routeTemplates"
	default:
		m.screen = "routeEditor"
	}
}

func (m Model) routingView() string {
	rows := []string{accent.Render(m.l("Маршрутизация Xray", "Xray routing")), ""}
	list := []string{}
	switch m.screen {
	case "routeProgress":
		rows = append(rows, m.l("Проверяю данные и выполняю операцию…", "Checking data and performing the operation…"))
	case "routeTemplates":
		rows = append(rows, m.l("Выберите существующий шаблон XRAY_JSON для изменения:", "Select an existing XRAY_JSON template to update:"))
		for _, value := range m.routes.Inventory.Templates {
			list = append(list, value.Name)
		}
	case "routeQuickNodes":
		rows = append(rows, m.l("РФ — напрямую, остальное — через отмеченные ноды.", "Russia direct; other traffic through selected nodes."), m.l("Пробел — отметить · Enter — применить", "Space — select · Enter — apply"))
		for _, node := range m.routes.Inventory.Nodes {
			mark := "[ ] "
			if m.routes.Selection[node.UUID] {
				mark = "[✓] "
			}
			label := mark + node.Name + " · " + node.Address
			if node.IsDisabled {
				label += m.l(" · отключена", " · disabled")
			}
			list = append(list, label)
		}
	case "routeEditor":
		rows = append(rows, m.l("Отдельный шаблон подписки; подключения подставляет панель.", "A separate subscription template; the panel injects connection details."))
		rows = append(rows, m.l("Схема РФ напрямую использует geoip:ru и geosite:category-ru. Клиенту нужны соответствующие базы GeoIP/GeoSite.", "The Russia-direct preset uses geoip:ru and geosite:category-ru. The client needs matching GeoIP/GeoSite assets."))
		list = []string{m.l("Имя: ", "Name: ") + m.routes.Spec.Name, fmt.Sprintf(m.l("Группы подключений: %d", "Connection groups: %d"), len(m.routes.Spec.Pools)), fmt.Sprintf(m.l("Дополнительные выходы WARP / мост: %d", "Extra WARP / bridge outbounds: %d"), len(m.routes.Spec.Exits)), fmt.Sprintf(m.l("Правила: %d", "Rules: %d"), len(m.routes.Spec.Rules)), m.l("По умолчанию: ", "Default: ") + m.routes.Spec.DefaultTarget, m.l("Предварительный просмотр и создание", "Preview and create")}
	case "routePools":
		for _, pool := range m.routes.Spec.Pools {
			list = append(list, fmt.Sprintf("%s · %d", pool.Name, len(pool.Hosts)))
		}
		list = append(list, m.l("Добавить группу", "Add group"))
	case "routeHosts":
		rows = append(rows, m.l("Пробел — выбрать подключения. Enter — сохранить группу.", "Space — select connections. Enter — save group."))
		for _, host := range m.routes.Inventory.Hosts {
			mark := "[ ] "
			if m.routes.Selection[host.UUID] {
				mark = "[✓] "
			}
			label := mark + host.Remark + " · " + host.Address
			if host.Disabled {
				label += " · " + m.l("отключено", "disabled")
			}
			list = append(list, label)
		}
	case "routeRules":
		rows = append(rows, m.l("Правила по порядку; Ctrl+↑/↓ — переместить, Del — удалить.", "Rules are ordered; Ctrl+↑/↓ — move, Del — delete."))
		for _, rule := range m.routes.Spec.Rules {
			conditions := append(append([]string(nil), rule.Domains...), rule.IPs...)
			conditions = append(conditions, rule.Protocols...)
			list = append(list, rule.Target+" ← "+strings.Join(conditions, ", ")+" "+rule.Network+" "+rule.Port)
		}
		list = append(list, m.l("Добавить правило", "Add rule"))
	case "routeTargets":
		rows = append(rows, m.l("Выберите выход для трафика:", "Select the outbound for traffic:"))
		list = m.routeTargets()
	case "routeExits":
		rows = append(rows, m.l("Это выходы на устройстве пользователя. Ключ WARP и VLESS-ссылка транзитного пользователя попадут в подписки получателей шаблона.", "These outbounds run on the user's device. The WARP key and service-user VLESS URI will be delivered to recipients of this template."))
		for _, exit := range m.routes.Spec.Exits {
			list = append(list, exit.Name+" · "+exit.Kind)
		}
		list = append(list, m.l("Добавить клиентский WARP / WireGuard", "Add client WARP / WireGuard"), m.l("Добавить VLESS-мост через входную ноду", "Add VLESS bridge through an entry node"))
	case "routeNameInput", "routePoolInput", "routeRuleInput", "routeExitInput":
		labels := []string{m.l("Имя", "Name")}
		if m.screen == "routeRuleInput" {
			labels = []string{m.l("Домены (через ;)", "Domains (semicolon-separated)"), m.l("IP/CIDR/geoip (через запятую)", "IP/CIDR/geoip (comma-separated)"), m.l("Протоколы (необязательно)", "Protocols (optional)"), m.l("Сеть (необязательно)", "Network (optional)"), m.l("Порты (необязательно)", "Ports (optional)")}
		}
		if m.screen == "routeExitInput" {
			labels = []string{m.l("Короткое имя выхода", "Short outbound name"), m.l("Путь к файлу WireGuard или VLESS-ссылки", "WireGuard or VLESS URI file path")}
			if m.routes.ExitKind == "vless" {
				labels = append(labels, m.l("Входная группа с одним подключением", "Entry group with one host"))
			}
		}
		for i, label := range labels {
			if i == m.focus {
				label = "› " + label
			} else {
				label = "  " + label
			}
			rows = append(rows, label, m.inputs[i].View())
		}
	case "routeConfirm":
		rows = append(rows, m.routes.Text, "", m.l("Enter — подтвердить · Esc — назад", "Enter — confirm · Esc — back"))
	case "routeList":
		for _, scheme := range m.routes.Inventory.Schemes {
			list = append(list, scheme.Name+" · "+m.routePhase(scheme.Phase))
		}
	case "routeDetails":
		if scheme := m.routes.Scheme; scheme != nil {
			rows = append(rows, scheme.Name, "UUID: "+scheme.TemplateUUID, m.l("Состояние: ", "State: ")+m.routePhase(scheme.Phase))
			list = []string{m.l("Продолжить / проверить", "Resume / check"), m.l("Удалить управляемый шаблон", "Delete managed template")}
		}
	case "routeResult":
		hint := m.l("Enter — список схем · Esc — в меню", "Enter — schemes · Esc — menu")
		if m.routes.Quick {
			hint = m.l("Enter / Esc — в меню", "Enter / Esc — menu")
		}
		rows = append(rows, m.routes.Text, "", hint)
	}
	if len(list) == 0 && (m.screen == "routeTemplates" || m.screen == "routeList" || m.screen == "routeHosts" || m.screen == "routeQuickNodes") {
		rows = append(rows, m.l("Список пуст.", "The list is empty."))
	}
	for i, line := range list {
		if i == m.routes.Cursor {
			rows = append(rows, accent.Render("› "+line))
		} else {
			rows = append(rows, "  "+line)
		}
	}
	return strings.Join(rows, "\n")
}

func (m Model) routePhase(phase string) string {
	values := map[string][2]string{"prepared": {"готова к созданию", "prepared"}, "creating": {"создание шаблона", "creating template"}, "configuring": {"настройка шаблона", "configuring template"}, "ready": {"готова", "ready"}, "removing": {"удаление", "removing"}, "removed": {"удалена", "removed"}}
	if labels, ok := values[phase]; ok {
		return m.l(labels[0], labels[1])
	}
	return m.l("неизвестно", "unknown")
}

func (m Model) showRouteInputs(values, placeholders []string, screen string) (tea.Model, tea.Cmd) {
	cmd := m.routeInputs(values, placeholders, screen)
	return m, cmd
}
