package ui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/ConsolePro/RemnaManager/internal/config"
	"github.com/ConsolePro/RemnaManager/internal/installer"
	"github.com/ConsolePro/RemnaManager/internal/services"
	"github.com/ConsolePro/RemnaManager/internal/system/stats"
	"github.com/ConsolePro/RemnaManager/internal/updater"
	"github.com/charmbracelet/bubbles/progress"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type statsMsg stats.Snapshot
type updateMsg struct {
	result updater.Result
	err    error
}
type savedMsg struct {
	cfg config.Config
	err error
}
type environmentMsg struct {
	info string
	err  error
	run  int
}
type stepMsg struct {
	run, step int
	err       error
}
type frame struct {
	items   []item
	cursor  int
	title   string
	titleEN string
}

type Model struct {
	ctx                                                context.Context
	store                                              config.Store
	cfg, draft                                         config.Config
	checker                                            updater.Checker
	service                                            services.PanelInstaller
	screen                                             string
	frames                                             []frame
	inputs                                             []textinput.Model
	focus, language                                    int
	width, height                                      int
	viewport                                           viewport.Model
	progress                                           progress.Model
	stats                                              stats.Snapshot
	statsProvider                                      func(context.Context) stats.Snapshot
	lastStats                                          time.Time
	latest, updateStatus, notice, problem, environment string
	checking, saving, exitConfirm, exitYes, editing    bool
	stage, run                                         int
	scroll                                             int
	logs                                               []string
	proxyChoice                                        int
	protectionChoice                                   int
	installCtx                                         context.Context
	installCancel                                      context.CancelFunc
	panelRequest                                       services.PanelRequest
	panelTitle                                         string
	panelResult                                        string
	customChoice                                       int
	nodeAction, nodeTitle, nodeFingerprint             string
	nodeRequest                                        services.NodeRequest
	nodeList                                           []services.Node
	nodeCursor                                         int
	nodeTarget                                         services.Node
	nodeSSH                                            services.NodeSSH
	nodeSSHChanged                                     bool
	nodeInstallParams                                  services.NodeInstallParameters
	nodeInstallJob                                     *services.NodeInstallStatus
	nodeInstallJobs                                    []services.NodeInstallStatus
	nodeInstallStage                                   int
}

func New(ctx context.Context, store config.Store, cfg config.Config, checker updater.Checker, service services.PanelInstaller) Model {
	m := Model{ctx: ctx, store: store, cfg: cfg, checker: checker, service: service, screen: "menu", width: 80, height: 30,
		frames: []frame{{items: menu()}}, viewport: viewport.New(74, 12), progress: progress.New(progress.WithSolidFill(emerald), progress.WithoutPercentage(), progress.WithFillCharacters('━', '━'), progress.WithColorProfile(lipgloss.ColorProfile())), statsProvider: stats.Collect}
	m.progress.EmptyColor = edge
	if !cfg.Initialized {
		m.startSettings(false)
	}
	return m
}
func (m Model) l(ru, en string) string {
	if m.cfg.Language == "en" {
		return en
	}
	return ru
}
func (m Model) Init() tea.Cmd {
	return tea.Batch(m.collect(), m.check(), textinput.Blink, headerClock())
}

type clockMsg struct{}

func headerClock() tea.Cmd {
	return tea.Tick(time.Second, func(time.Time) tea.Msg { return clockMsg{} })
}
func (m Model) collect() tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(m.ctx, 1500*time.Millisecond)
		defer cancel()
		return statsMsg(m.statsProvider(ctx))
	}
}
func (m Model) check() tea.Cmd {
	return func() tea.Msg { r, e := m.checker.Check(m.ctx); return updateMsg{r, e} }
}
func newInput(placeholder, value string) textinput.Model {
	t := textinput.New()
	t.Placeholder = placeholder
	t.CharLimit = 254
	t.Width = 48
	t.SetValue(value)
	return t
}
func (m *Model) startSettings(editing bool) {
	m.editing = editing
	m.draft = m.cfg
	m.language = 0
	if m.cfg.Language == "en" {
		m.language = 1
	}
	m.inputs = []textinput.Model{newInput("admin@example.com", m.cfg.Email)}
	m.focus = 0
	m.screen = "language"
	m.problem = ""
}
func (m *Model) back() {
	m.problem = ""
	m.scroll = 0
	switch m.screen {
	case "nodeInstallInput", "nodeInstallPending", "nodeInstallResult":
		m.cancelInstall()
		m.run++
		m.screen = "menu"
	case "nodeInstallConfirm":
		m.screen = "nodesTrust"
	case "nodeInstallProgress", "nodeInstallVerify", "nodeInstallPanelIP":
		m.cancelInstall()
		m.run++
		m.screen = "nodeInstallResult"
		m.panelResult = m.l("Дальнейшие этапы отменены. Установка сохранена в «Незавершённые установки».\nЕсли firewall ожидает подтверждения, таймер автоматически вернёт прежние правила.", "Further stages cancelled. Resume from Pending installations.\nIf firewall confirmation is pending, its timer restores the previous rules.")
	case "nodesList":
		m.cancelInstall()
		m.run++
		m.screen = "menu"
	case "nodesProgress":
		m.cancelInstall()
		m.run++
		if len(m.nodeList) > 0 {
			m.screen = "nodesList"
		} else {
			m.screen = "menu"
		}
	case "nodesInput", "nodesResult":
		m.screen = "nodesList"
	case "nodesTrust":
		if m.nodeAction == "node-update" || m.nodeAction == "node-reinstall" {
			m.screen = "nodesList"
			break
		}
		if strings.HasPrefix(m.nodeAction, "node-install-") {
			if m.nodeInstallJob != nil {
				m.screen = "nodeInstallPending"
			} else {
				m.prepareNodeInstallInputs()
				m.screen = "nodeInstallInput"
			}
		} else {
			m.prepareNodeSSHInputs()
			m.screen = "nodesInput"
		}
	case "nodesConfirm":
		if m.nodeAction == "node-ssh" || m.nodeAction == "node-update" || m.nodeAction == "node-reinstall" {
			m.screen = "nodesTrust"
		} else if m.nodeAction == "node-rename" {
			m.screen = "nodesInput"
			m.inputs[0].Focus()
		} else {
			m.screen = "nodesList"
		}
	case "panelProgress":
		m.cancelInstall()
		m.run++
		m.screen = "notice"
		m.notice = m.l("Операция отменяется. Выполненные изменения сохраняются; проверьте статус и приватный журнал.", "Operation is being cancelled. Completed changes remain; inspect status and the private log.")
	case "panelConfirm":
		if m.panelRequest.Action == "panel-protection" && m.panelRequest.Protection != "tinyauth" {
			m.screen = "panelProtection"
		} else if m.panelRequest.Action == "sub-image" && m.customChoice == 0 {
			m.screen = "panelCustom"
		} else if len(m.inputs) > 0 && (m.panelRequest.Action == "panel-token" || m.panelRequest.Action == "panel-update" || m.panelRequest.Action == "sub-image" || m.panelRequest.Action == "panel-protection") {
			m.screen = "panelInput"
			m.inputs[0].Focus()
		} else {
			m.screen = "menu"
		}
	case "menu":
		if len(m.frames) > 1 {
			m.frames = m.frames[:len(m.frames)-1]
		}
	case "email":
		m.screen = "language"
	case "language":
		if m.editing {
			m.screen = "menu"
			m.cfg.Language = m.draft.Language
		}
	case "environment":
		m.run++
		m.screen = "menu"
	case "domains":
		if m.liveInstall() {
			m.screen = "protectionOptions"
		} else {
			m.screen = "environment"
		}
	case "installOptions":
		m.screen = "environment"
	case "protectionOptions":
		m.screen = "installOptions"
	case "panelProtection":
		m.screen = "menu"
	case "panelInput":
		if m.panelRequest.Action == "panel-protection" {
			m.screen = "panelProtection"
		} else {
			m.screen = "menu"
		}
	case "confirm":
		m.screen = "domains"
		m.inputs[m.focus].Focus()
	case "progress":
		m.cancelInstall()
		m.run++
		m.screen = "menu"
	case "firewallVerify":
		m.cancelInstall()
		m.screen = "notice"
		m.notice = m.l("Подтверждение не получено. Firewall будет автоматически отменён по таймеру.", "Not confirmed. Firewall will be rolled back automatically by its timer.")
	default:
		m.screen = "menu"
	}
	m.viewport.GotoTop()
}
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case nodeResultMsg:
		return m.receiveNodeResult(msg)
	case panelResultMsg:
		return m.receivePanelResult(msg)
	case confirmedInstallMsg:
		m.saving = false
		if msg.err != nil {
			m.problem = msg.err.Error()
		} else {
			m.screen = "installResult"
			m.problem = ""
		}
	case clockMsg:
		// Repaint delayed-data status even while a collector is slow or blocked.
		return m, headerClock()
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.viewport.Width = max(1, msg.Width-6)
		m.viewport.Height = max(1, msg.Height-15)
		m.progress.Width = max(1, min(60, msg.Width-12))
		for i := range m.inputs {
			m.inputs[i].Width = max(1, min(48, msg.Width-12))
		}
	case statsMsg:
		m.stats = stats.Snapshot(msg)
		m.lastStats = time.Now()
		return m, tea.Tick(time.Second, func(time.Time) tea.Msg { return pollStats{} })
	case pollStats:
		return m, m.collect()
	case updateMsg:
		if msg.err != nil {
			m.updateStatus = m.l("GitHub недоступен; можно продолжать работу.", "GitHub unavailable; you can continue working.")
		} else {
			m.latest = msg.result.Version
			m.updateStatus = m.l("Проверка завершена.", "Check complete.")
		}
	case savedMsg:
		m.saving = false
		if msg.err != nil {
			m.problem = msg.err.Error()
		} else {
			m.cfg = msg.cfg
			m.screen = "menu"
			m.problem = ""
		}
	case environmentMsg:
		if msg.run != m.run {
			return m, nil
		}
		m.checking = false
		m.environment = msg.info
		if msg.err != nil {
			m.problem = msg.err.Error()
		} else if s, ok := m.liveInstaller(); ok && s.Installed() {
			m.screen = "installResult"
		}
	case stepMsg:
		if msg.run != m.run || m.screen != "progress" {
			return m, nil
		}
		if msg.err != nil {
			m.problem = msg.err.Error()
			return m, nil
		}
		label := m.service.Stages()[msg.step]
		prefix := m.l("Демо: ", "Demo: ")
		if m.liveInstall() {
			prefix = "✓ "
		}
		m.logs = append(m.logs, prefix+m.l(label[0], label[1]))
		m.stage = msg.step + 1
		if m.stage == len(m.service.Stages()) {
			m.scroll = 0
			if m.liveInstall() {
				m.screen = "firewallVerify"
			}
		}
		if m.stage < len(m.service.Stages()) {
			return m, m.nextStep()
		}
	case tea.KeyMsg:
		key := msg.String()
		if key == "ctrl+c" || key == "f1" {
			m.exitConfirm = !m.exitConfirm
			m.exitYes = false
			return m, nil
		}
		if m.exitConfirm {
			switch key {
			case "left", "right", "tab", "shift+tab", "up", "down":
				m.exitYes = !m.exitYes
			case "esc":
				m.exitConfirm = false
			case "enter":
				if m.exitYes && !m.saving {
					m.cancelInstall()
					return m, tea.Quit
				}
				m.exitConfirm = false
			}
			return m, nil
		}
		if m.saving {
			return m, nil
		}
		if key == "esc" {
			m.back()
			return m, nil
		}
		if m.screen != "menu" && m.screen != "email" && m.screen != "domains" {
			if key == "pgdown" {
				m.scroll = min(m.scroll+3, max(0, strings.Count(m.viewportBody(), "\n")-2))
				return m, nil
			}
			if key == "pgup" {
				m.scroll = max(0, m.scroll-3)
				return m, nil
			}
		}
		switch m.screen {
		case "menu":
			f := &m.frames[len(m.frames)-1]
			switch key {
			case "up", "shift+tab":
				f.cursor = (f.cursor + len(f.items) - 1) % len(f.items)
			case "down", "tab":
				f.cursor = (f.cursor + 1) % len(f.items)
			case "left":
				m.back()
			case "enter", "right":
				selected := f.items[f.cursor]
				if len(selected.children) > 0 {
					m.frames = append(m.frames, frame{items: selected.children, title: selected.ru, titleEN: selected.en})
					m.viewport.GotoTop()
					break
				}
				switch selected.action {
				case "settings":
					m.startSettings(true)
				case "install":
					m.cancelInstall()
					m.run++
					m.environment = ""
					m.problem = ""
					m.checking = true
					m.screen = "environment"
					run := m.run
					return m, func() tea.Msg { info, e := m.service.Check(m.ctx); return environmentMsg{info, e, run} }
				case "check":
					m.updateStatus = m.l("Проверка…", "Checking…")
					return m, m.check()
				default:
					if strings.HasPrefix(selected.action, "node") {
						return m.openNodeOperation(selected)
					}
					if strings.HasPrefix(selected.action, "panel-") || strings.HasPrefix(selected.action, "sub-") {
						return m.openPanelOperation(selected)
					}
					m.notice = m.l(selected.ru, selected.en) + "\n\n" + m.l("В разработке. Операция пока не реализована.", "In development. This operation is not implemented yet.")
					m.screen = "notice"
				}
			}
		case "nodeInstallInput", "nodeInstallConfirm", "nodeInstallVerify", "nodeInstallPending", "nodeInstallResult", "nodeInstallPanelIP":
			return m.updateNodeInstallScreen(msg)
		case "nodesList", "nodesInput", "nodesTrust", "nodesConfirm", "nodesResult":
			return m.updateNodeScreen(msg)
		case "panelInput", "panelConfirm", "panelResult", "panelCustom", "panelProtection":
			return m.updatePanelScreen(msg)
		case "language":
			switch key {
			case "up", "down", "left", "right", "tab", "shift+tab":
				m.language = 1 - m.language
			case "enter":
				m.cfg.Language = []string{"ru", "en"}[m.language]
				m.screen = "email"
				return m, m.inputs[0].Focus()
			}
		case "email":
			if key == "enter" {
				email := strings.TrimSpace(m.inputs[0].Value())
				if !config.ValidEmail(email) {
					m.problem = m.l("Введите корректный email.", "Enter a valid email address.")
					break
				}
				c := config.Config{Language: []string{"ru", "en"}[m.language], Email: email, Initialized: true}
				m.saving = true
				return m, func() tea.Msg { return savedMsg{c, m.store.Save(c)} }
			}
		case "environment":
			if key == "enter" && m.environment != "" && m.problem == "" {
				m.prepareInstallInputs()
				if m.liveInstall() {
					m.screen = "installOptions"
					return m, nil
				}
				m.screen = "domains"
				return m, m.inputs[0].Focus()
			}
		case "domains":
			if key == "tab" || key == "shift+tab" || key == "up" || key == "down" {
				m.inputs[m.focus].Blur()
				if key == "shift+tab" || key == "up" {
					m.focus = (m.focus + len(m.inputs) - 1) % len(m.inputs)
				} else {
					m.focus = (m.focus + 1) % len(m.inputs)
				}
				return m, m.inputs[m.focus].Focus()
			}
			if key == "enter" {
				if m.focus < len(m.inputs)-1 {
					m.inputs[m.focus].Blur()
					m.focus++
					return m, m.inputs[m.focus].Focus()
				}
				p := m.parameters()
				if m.liveInstall() {
					if err := installer.ValidateParameters(p); err != nil {
						m.problem = err.Error()
						break
					}
				}
				if !installer.ValidDomain(p.PanelDomain) || !installer.ValidDomain(p.SubscriptionDomain) || p.PanelDomain == p.SubscriptionDomain {
					m.problem = m.l("Укажите два разных домена без https:// и пути (IDN: punycode).", "Enter two different domains without https:// or path (IDN: punycode).")
					break
				}
				m.problem = ""
				m.screen = "confirm"
			}
		case "confirm":
			if key == "enter" {
				m.cancelInstall()
				m.installCtx, m.installCancel = context.WithCancel(m.ctx)
				m.stage = 0
				m.logs = nil
				m.screen = "progress"
				m.run++
				return m, m.nextStep()
			}
		case "progress":
			if key == "enter" && (m.stage == len(m.service.Stages()) || m.problem != "") {
				m.screen = "menu"
				m.problem = ""
			}
		case "notice":
			if key == "enter" {
				m.screen = "menu"
			}
		case "installOptions":
			if key == "left" || key == "right" || key == "up" || key == "down" || key == "tab" || key == "shift+tab" {
				m.proxyChoice = 1 - m.proxyChoice
			}
			if key == "enter" {
				m.screen = "protectionOptions"
			}
		case "protectionOptions":
			if key == "left" || key == "right" || key == "up" || key == "down" || key == "tab" || key == "shift+tab" {
				m.moveProtectionChoice(key)
			}
			if key == "enter" {
				m.prepareProtectionInput()
				m.screen = "domains"
				return m, m.inputs[0].Focus()
			}
		case "firewallVerify":
			if key == "enter" && m.problem == "" {
				m.saving = true
				return m, m.confirmFirewall()
			}
		case "installResult":
			if key == "enter" {
				m.screen = "menu"
			}
		}
	}
	if m.screen == "email" || m.screen == "domains" || m.screen == "panelInput" || m.screen == "nodesInput" || m.screen == "nodesTrust" || m.screen == "nodeInstallInput" || m.screen == "nodeInstallPanelIP" {
		var cmd tea.Cmd
		m.inputs[m.focus], cmd = m.inputs[m.focus].Update(msg)
		return m, cmd
	}
	return m, nil
}

type pollStats struct{}

func (m Model) parameters() services.PanelParameters {
	return m.installationParameters()
}
func (m Model) nextStep() tea.Cmd {
	run, step, p := m.run, m.stage, m.parameters()
	ctx := m.installCtx
	if ctx == nil {
		ctx = m.ctx
	}
	return func() tea.Msg {
		c, cancel := context.WithTimeout(ctx, 20*time.Minute)
		defer cancel()
		return stepMsg{run, step, m.service.Step(c, step, p)}
	}
}

func (m Model) title() string {
	parts := []string{m.l("Главное меню", "Main menu")}
	for _, f := range m.frames[1:] {
		parts = append(parts, m.l(f.title, f.titleEN))
	}
	return strings.Join(parts, " › ")
}

func gb(n uint64) float64 { return float64(n) / (1 << 30) }
func (m Model) statsView() string {
	if m.lastStats.IsZero() {
		return muted.Render(m.l("Получаем текущие данные сервера…", "Reading current server metrics…"))
	}
	s := m.stats
	cores := m.l(stats.Cores(s.CPUs), "cores")
	cpu := accent.Render(fmt.Sprintf("%.0f%%", s.CPU))
	first := fmt.Sprintf("%s %d %s · %s", m.l("ЦП", "CPU"), s.CPUs, cores, cpu)
	ram := fmt.Sprintf(m.l("ОЗУ %.1f / %.1f GB доступно", "RAM %.1f / %.1f GB available"), gb(s.Available), gb(s.Memory))
	second := fmt.Sprintf(m.l("Диск %.1f GB свободно из %.1f · %.0f%% · SWAP %.1f GB", "Disk %.1f GB free of %.1f · %.0f%% · SWAP %.1f GB"), gb(s.Free), gb(s.Disk), s.DiskPercent, gb(s.Swap))
	network := fmt.Sprintf("IPv4 %s  ·  IPv6 %s", s.IPv4, s.IPv6)
	if m.contentWidth() < 58 {
		return first + "\n" + muted.Render(ram) + "\n" + muted.Render(second) + "\n" + muted.Render(network)
	}
	return first + "  ·  " + muted.Render(ram) + "\n" + muted.Render(second) + "\n" + muted.Render(network)
}
