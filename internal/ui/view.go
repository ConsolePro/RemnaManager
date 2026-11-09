package ui

import (
	"fmt"
	"github.com/ConsolePro/RemnaManager/internal/updater"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"strings"
)

var accent = lipgloss.NewStyle().Foreground(lipgloss.Color("42")).Bold(true)
var muted = lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
var warning = lipgloss.NewStyle().Foreground(lipgloss.Color("214"))
var failure = lipgloss.NewStyle().Foreground(lipgloss.Color("196"))

func (m Model) body() string {
	switch m.screen {
	case "menu":
		f := m.frames[len(m.frames)-1]
		rows := []string{accent.Render(m.title()), ""}
		for i, item := range f.items {
			text := m.l(item.ru, item.en)
			if len(item.children) > 0 {
				text += " ›"
			} else if item.action == "pending" {
				text += " · " + m.l("В разработке", "In development")
			} else if item.action == "install" {
				text += " · " + m.l("Демо", "Demo")
			}
			if i == f.cursor {
				text = accent.Render("› " + text)
			} else {
				text = "  " + text
			}
			rows = append(rows, text)
		}
		if m.updateStatus != "" {
			rows = append(rows, "", muted.Render(m.updateStatus))
		}
		return strings.Join(rows, "\n")
	case "language":
		rows := []string{"Язык / Language", ""}
		for i, s := range []string{"Русский", "English"} {
			if i == m.language {
				rows = append(rows, accent.Render("› "+s))
			} else {
				rows = append(rows, "  "+s)
			}
		}
		return strings.Join(rows, "\n")
	case "email":
		status := ""
		if m.saving {
			status = m.l("Сохранение…", "Saving…")
		}
		return m.l("Email администратора для ACME", "Administrator email for ACME") + "\n\n" + m.inputs[0].View() + "\n\n" + m.l("Enter — сохранить настройки", "Enter — save settings") + "\n" + status
	case "environment":
		status := m.environment
		if m.checking {
			status = m.l("Проверка окружения…", "Checking environment…")
		}
		return accent.Render(m.l("Установка панели · ДЕМО", "Panel installation · DEMO")) + "\n\n" + status + "\n\n" + warning.Render(m.l("Все этапы фиктивные. Сервисы и файлы не изменяются.", "All stages are simulated. Services and files are not changed.")) + "\n\n" + m.l("Enter — продолжить", "Enter — continue")
	case "domains":
		return m.l("Домены панели и подписки · ДЕМО", "Panel and subscription domains · DEMO") + "\n\n" + m.l("Панель:", "Panel:") + "\n" + m.inputs[0].View() + "\n\n" + m.l("Подписка:", "Subscription:") + "\n" + m.inputs[1].View() + "\n\n" + m.l("Tab — поле · Enter — далее", "Tab — field · Enter — next")
	case "confirm":
		p := m.parameters()
		return m.l("Подтверждение демонстрации", "Confirm demonstration") + fmt.Sprintf("\n\nPanel: %s\nSubscription: %s\nEmail: %s\n\n", p.PanelDomain, p.SubscriptionDomain, m.cfg.Email) + warning.Render(m.l("Реальная установка не выполняется.", "No actual installation will be performed.")) + "\n\n" + m.l("Enter — запустить демо · Esc — изменить", "Enter — run demo · Esc — edit")
	case "progress":
		steps := m.service.Stages()
		status := m.l("Демонстрация завершена. Enter — в меню.", "Demonstration complete. Enter — return to menu.")
		if m.stage < len(steps) {
			status = m.l(steps[m.stage][0], steps[m.stage][1])
		}
		percent := float64(m.stage) / float64(len(steps))
		rows := []string{accent.Render(m.l("Демонстрационная установка", "Demo installation")), accent.Render(status), m.progress.ViewAs(percent), fmt.Sprintf("%.0f%%", percent*100), ""}
		for i, step := range steps {
			prefix := "○ "
			if i < m.stage {
				prefix = "✓ "
			} else if i == m.stage {
				prefix = "● "
			}
			rows = append(rows, prefix+m.l(step[0], step[1]))
		}
		rows = append(rows, "", m.l("Журнал (демонстрация)", "Log (simulation)"))
		rows = append(rows, m.logs...)
		if m.stage == len(steps) {
			rows = append(rows, "", accent.Render(m.l("Демонстрация завершена. Enter — в меню.", "Demonstration complete. Enter — return to menu.")))
		}
		return strings.Join(rows, "\n")
	case "notice":
		return m.notice + "\n\n" + m.l("Enter / Esc — в меню", "Enter / Esc — return to menu")
	}
	return ""
}

func (m Model) View() string {
	if m.width < 36 || m.height < 18 {
		return m.l("Увеличьте терминал до 36×18.\nF1 — выход, Ctrl+C — выход.", "Resize terminal to 36×18.\nF1 — exit, Ctrl+C — exit.") + m.exitView()
	}
	version := updater.Version + " · " + m.l("предварительная", "preview")
	if updater.Newer(m.latest, updater.Version) {
		version += " · " + m.l("Доступна ", "Available ") + m.latest + " · [" + m.l("Обновить: в разработке", "Update: in development") + "]"
	}
	width := m.width - 6
	logo := accent.Render("REMNA MANAGER")
	header := logo + strings.Repeat(" ", max(1, width-lipgloss.Width(logo)-lipgloss.Width(version))) + muted.Render(version)
	if lipgloss.Width(header) > width {
		header = logo + "\n" + muted.Render(version)
	}
	body := m.body()
	if m.problem != "" {
		body += "\n\n" + failure.Render(m.problem)
	}
	// Crop long lines before viewport rendering; never split UTF-8 or ANSI sequences.
	lines := strings.Split(body, "\n")
	for i, line := range lines {
		lines[i] = ansi.Truncate(line, width, "…")
	}
	vp := m.viewport
	vp.Height = max(1, m.height-strings.Count(header, "\n")-13)
	vp.SetContent(strings.Join(lines, "\n"))
	if m.screen != "menu" {
		vp.SetYOffset(m.scroll)
	}
	if m.screen == "menu" {
		cursor := m.frames[len(m.frames)-1].cursor + 2
		if cursor >= vp.YOffset+vp.Height {
			vp.SetYOffset(cursor - vp.Height + 1)
		}
		if cursor < vp.YOffset {
			vp.SetYOffset(cursor)
		}
	} else if m.problem != "" {
		vp.GotoBottom()
	} else if m.screen == "domains" {
		if m.focus == 1 && vp.Height < 7 {
			vp.SetYOffset(7 - vp.Height)
		}
	}
	hint := m.l("↑↓ выбор · ←→ раздел · Enter · Esc назад · F1 выход", "↑↓ select · ←→ section · Enter · Esc back · F1 exit")
	if m.screen != "menu" {
		hint = m.l("Tab / Shift+Tab поле · Enter далее · Esc назад · F1 выход", "Tab / Shift+Tab field · Enter next · Esc back · F1 exit")
	}
	if m.screen == "progress" {
		hint = m.l("PgUp/PgDn журнал · Enter в меню · Esc назад · F1 выход", "PgUp/PgDn log · Enter menu · Esc back · F1 exit")
	}
	content := header + "\n\n" + muted.Render(m.statsView()) + "\n\n" + vp.View() + "\n\n" + muted.Render(ansi.Truncate(hint, width, "…"))
	if m.exitConfirm {
		content = m.exitView() + "\n\n" + content
	}
	lines = strings.Split(content, "\n")
	for i, line := range lines {
		lines[i] = ansi.Truncate(line, width, "…")
	}
	if len(lines) > m.height-2 {
		lines = lines[:m.height-2]
	}
	return lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("42")).Padding(0, 1).Width(m.width - 2).Render(strings.Join(lines, "\n"))
}
func (m Model) exitView() string {
	if !m.exitConfirm {
		return ""
	}
	no, yes := m.l("Нет", "No"), m.l("Да", "Yes")
	if m.exitYes {
		yes = accent.Render("[" + yes + "]")
	} else {
		no = accent.Render("[" + no + "]")
	}
	return m.l("Выйти?", "Exit?") + "  " + no + " / " + yes + "  ← → Enter · Esc"
}
