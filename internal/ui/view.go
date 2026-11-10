package ui

import (
	"fmt"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"strings"
	"time"
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
				if m.liveInstall() {
					text += " · " + m.l("Реальная установка", "Real installation")
				} else {
					text += " · " + m.l("Демо", "Demo")
				}
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
		if m.liveInstall() {
			return accent.Render(m.l("Установка панели и компонентов", "Panel and component installation")) + "\n\n" + status + "\n\n" + m.l("Требуются два домена с A/AAAA на этот сервер и доступные TCP 80/443.\nСуществующие установки не перезаписываются.", "Two domains must resolve to this server; TCP 80/443 must be reachable.\nExisting unmanaged installations are not overwritten.") + "\n\n" + m.l("Enter — продолжить", "Enter — continue")
		}
		return accent.Render(m.l("Установка панели · ДЕМО", "Panel installation · DEMO")) + "\n\n" + status + "\n\n" + warning.Render(m.l("Все этапы фиктивные. Сервисы и файлы не изменяются.", "All stages are simulated. Services and files are not changed.")) + "\n\n" + m.l("Enter — продолжить", "Enter — continue")
	case "domains":
		if m.liveInstall() {
			return m.realDomainsView()
		}
		return m.l("Домены панели и подписки · ДЕМО", "Panel and subscription domains · DEMO") + "\n\n" + m.l("Панель:", "Panel:") + "\n" + m.inputs[0].View() + "\n\n" + m.l("Подписка:", "Subscription:") + "\n" + m.inputs[1].View() + "\n\n" + m.l("Tab — поле · Enter — далее", "Tab — field · Enter — next")
	case "confirm":
		if m.liveInstall() {
			return m.realConfirmationView()
		}
		p := m.parameters()
		return m.l("Подтверждение демонстрации", "Confirm demonstration") + fmt.Sprintf("\n\nPanel: %s\nSubscription: %s\nEmail: %s\n\n", p.PanelDomain, p.SubscriptionDomain, m.cfg.Email) + warning.Render(m.l("Реальная установка не выполняется.", "No actual installation will be performed.")) + "\n\n" + m.l("Enter — запустить демо · Esc — изменить", "Enter — run demo · Esc — edit")
	case "progress":
		steps := m.service.Stages()
		status := m.l("Демонстрация завершена. Enter — в меню.", "Demonstration complete. Enter — return to menu.")
		if m.liveInstall() {
			status = m.l("Установка завершена. Требуется подтверждение SSH.", "Installation finished. SSH confirmation is required.")
		}
		if m.stage < len(steps) {
			status = m.l(steps[m.stage][0], steps[m.stage][1])
		}
		percent := float64(m.stage) / float64(len(steps))
		title := m.l("Демонстрационная установка", "Demo installation")
		if m.liveInstall() {
			title = m.l("Реальная установка", "Real installation")
		}
		rows := []string{accent.Render(title), accent.Render(status), m.classicProgressView(percent), fmt.Sprintf("%.0f%%", percent*100), ""}
		for i, step := range steps {
			prefix := "○ "
			if i < m.stage {
				prefix = "✓ "
			} else if i == m.stage {
				prefix = "● "
			}
			rows = append(rows, prefix+m.l(step[0], step[1]))
		}
		journal := m.l("Журнал (демонстрация)", "Log (simulation)")
		if m.liveInstall() {
			journal = m.l("Журнал этапов", "Stage log")
		}
		rows = append(rows, "", journal)
		rows = append(rows, m.logs...)
		if m.stage == len(steps) && !m.liveInstall() {
			rows = append(rows, "", accent.Render(m.l("Демонстрация завершена. Enter — в меню.", "Demonstration complete. Enter — return to menu.")))
		}
		return strings.Join(rows, "\n")
	case "panelInput", "panelConfirm", "panelProgress", "panelResult", "panelCustom":
		return m.panelScreenView()
	case "notice":
		return m.notice + "\n\n" + m.l("Enter / Esc — в меню", "Enter / Esc — return to menu")
	case "installOptions":
		return m.installOptionsView()
	case "firewallVerify":
		if m.saving {
			return m.l("Сохраняем подтверждённые правила…", "Saving confirmed firewall rules…")
		}
		s, _ := m.liveInstaller()
		return warning.Render(m.l("ПРОВЕРЬТЕ SSH ВО ВТОРОЙ СЕССИИ", "VERIFY SSH IN A SECOND SESSION")) + "\n\n" + m.l("Откройте новое SSH-подключение и проверьте HTTPS панели.\nEnter — подтвердить доступ и сохранить firewall.\nБез подтверждения правила автоматически отменятся через 120 секунд.\nEsc — оставить автоматический откат.", "Open a new SSH connection and verify the panel's HTTPS.\nEnter — confirm access and persist the firewall.\nWithout confirmation the rules roll back after 120 seconds.\nEsc — leave automatic rollback armed.") + "\n\n" + s.Summary()
	case "installResult":
		s, _ := m.liveInstaller()
		return accent.Render(m.l("Панель и подписка установлены", "Panel and subscription installed")) + "\n\n" + s.Summary() + "\n\n" + m.l("Enter — в меню", "Enter — return to menu")
	}
	return ""
}

func (m Model) viewportBody() string {
	body := m.body()
	if m.problem != "" {
		body += "\n\n" + failure.Render(m.problem)
	}
	if strings.HasPrefix(m.screen, "panel") && m.screen != "panelInput" {
		body = ansi.Hardwrap(body, max(1, m.width-6), true)
	}
	return body
}

func (m Model) View() string {
	if m.width < 36 || m.height < 18 {
		return m.l("Увеличьте терминал до 36×18.\nF1 — выход, Ctrl+C — выход.", "Resize terminal to 36×18.\nF1 — exit, Ctrl+C — exit.") + m.exitView()
	}
	width := m.width - 6
	header := m.serverHeader(time.Now())
	top := header + "\n\n" + m.classicStatsView()
	if m.exitConfirm {
		top = m.exitView() + "\n\n" + top
	}
	body := m.viewportBody()
	// Crop long lines before viewport rendering; never split UTF-8 or ANSI sequences.
	lines := strings.Split(body, "\n")
	for i, line := range lines {
		lines[i] = ansi.Truncate(line, width, "…")
	}
	vp := m.viewport
	vp.Width = width
	vp.Height = max(1, m.height-lipgloss.Height(top)-5)
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
	} else if m.problem != "" && m.screen != "panelResult" {
		vp.GotoBottom()
	} else if m.screen == "domains" {
		if m.liveInstall() {
			cursor := m.focus + 2
			if cursor >= vp.YOffset+vp.Height {
				vp.SetYOffset(cursor - vp.Height + 1)
			} else if cursor < vp.YOffset {
				vp.SetYOffset(cursor)
			}
		} else if m.focus == 1 && vp.Height < 7 {
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
	switch m.screen {
	case "panelInput":
		hint = m.l("Enter — продолжить · Esc — назад · F1 — выход", "Enter — continue · Esc — back · F1 — exit")
	case "panelConfirm":
		hint = m.l("Enter — выполнить · Esc — отменить · F1 — выход", "Enter — execute · Esc — cancel · F1 — exit")
	case "panelProgress":
		hint = m.l("Esc — отменить операцию · F1 — выход", "Esc — cancel operation · F1 — exit")
	case "panelResult":
		hint = m.l("PgUp/PgDn — прокрутка · Enter / Esc — в меню", "PgUp/PgDn — scroll · Enter / Esc — menu")
	case "panelCustom":
		hint = m.l("↑↓ — вариант · Enter — выбрать · Esc — назад", "↑↓ — option · Enter — select · Esc — back")
	}
	content := top + "\n\n" + vp.View() + "\n\n" + muted.Render(ansi.Truncate(hint, width, "…"))
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
