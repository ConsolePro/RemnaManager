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
	latest, updateStatus, notice, problem, environment string
	checking, saving, exitConfirm, exitYes, editing    bool
	stage, run                                         int
	scroll                                             int
	logs                                               []string
}

func New(ctx context.Context, store config.Store, cfg config.Config, checker updater.Checker, service services.PanelInstaller) Model {
	m := Model{ctx: ctx, store: store, cfg: cfg, checker: checker, service: service, screen: "menu", width: 80, height: 30,
		frames: []frame{{items: menu()}}, viewport: viewport.New(74, 12), progress: progress.New(progress.WithDefaultGradient())}
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
func (m Model) Init() tea.Cmd { return tea.Batch(m.collect(), m.check(), textinput.Blink) }
func (m Model) collect() tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(m.ctx, 1500*time.Millisecond)
		defer cancel()
		return statsMsg(stats.Collect(ctx))
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
		m.screen = "environment"
	case "confirm":
		m.screen = "domains"
		m.inputs[m.focus].Focus()
	case "progress":
		m.run++
		m.screen = "menu"
	default:
		m.screen = "menu"
	}
	m.viewport.GotoTop()
}
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
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
		return m, tea.Tick(2*time.Second, func(time.Time) tea.Msg { return pollStats{} })
	case pollStats:
		return m, m.collect()
	case updateMsg:
		m.checking = false
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
		m.logs = append(m.logs, m.l("Демо: ", "Demo: ")+m.l(label[0], label[1]))
		m.stage = msg.step + 1
		if m.stage == len(m.service.Stages()) {
			m.scroll = 0
		}
		if m.stage < len(m.service.Stages()) {
			return m, m.nextStep()
		}
	case tea.KeyMsg:
		key := msg.String()
		if key == "ctrl+c" {
			return m, tea.Quit
		}
		if key == "f1" {
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
				m.scroll = min(m.scroll+3, max(0, strings.Count(m.body(), "\n")-2))
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
					m.notice = m.l(selected.ru, selected.en) + "\n\n" + m.l("В разработке. Операция пока не реализована.", "In development. This operation is not implemented yet.")
					m.screen = "notice"
				}
			}
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
				m.inputs = []textinput.Model{newInput("panel.example.com", ""), newInput("sub.example.com", "")}
				m.focus = 0
				m.screen = "domains"
				return m, m.inputs[0].Focus()
			}
		case "domains":
			if key == "tab" || key == "shift+tab" || key == "up" || key == "down" {
				m.inputs[m.focus].Blur()
				m.focus = 1 - m.focus
				return m, m.inputs[m.focus].Focus()
			}
			if key == "enter" {
				if m.focus == 0 {
					m.inputs[0].Blur()
					m.focus = 1
					return m, m.inputs[1].Focus()
				}
				p := m.parameters()
				if !installer.ValidDomain(p.PanelDomain) || !installer.ValidDomain(p.SubscriptionDomain) || p.PanelDomain == p.SubscriptionDomain {
					m.problem = m.l("Укажите два разных домена без https:// и пути (IDN: punycode).", "Enter two different domains without https:// or path (IDN: punycode).")
					break
				}
				m.problem = ""
				m.screen = "confirm"
			}
		case "confirm":
			if key == "enter" {
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
		}
	}
	if m.screen == "email" || m.screen == "domains" {
		var cmd tea.Cmd
		m.inputs[m.focus], cmd = m.inputs[m.focus].Update(msg)
		return m, cmd
	}
	return m, nil
}

type pollStats struct{}

func (m Model) parameters() services.PanelParameters {
	return services.PanelParameters{PanelDomain: strings.ToLower(strings.TrimSpace(m.inputs[0].Value())), SubscriptionDomain: strings.ToLower(strings.TrimSpace(m.inputs[1].Value()))}
}
func (m Model) nextStep() tea.Cmd {
	run, step, p := m.run, m.stage, m.parameters()
	return func() tea.Msg { return stepMsg{run, step, m.service.Step(m.ctx, step, p)} }
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
	s := m.stats
	text := fmt.Sprintf(m.l("ЦП: %d %s · %.0f%%\nОЗУ: %.1f GB всего · %.1f GB доступно · SWAP %.1f GB\nДиск /: %.1f GB всего · %.1f GB свободно · %.0f%% занято", "CPU: %d %s · %.0f%%\nRAM: %.1f GB total · %.1f GB available · SWAP %.1f GB\nDisk /: %.1f GB total · %.1f GB free · %.0f%% used"), s.CPUs, m.l(stats.Cores(s.CPUs), "cores"), s.CPU, gb(s.Memory), gb(s.Available), gb(s.Swap), gb(s.Disk), gb(s.Free), s.DiskPercent)
	text += fmt.Sprintf("\nIPv4: %s · IPv6: %s", s.IPv4, s.IPv6)
	if s.Err != nil {
		text += "\n" + m.l("Часть статистики недоступна.", "Some statistics are unavailable.")
	}
	return text
}
