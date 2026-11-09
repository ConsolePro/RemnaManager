package ui

import (
	"fmt"
	"strings"

	"github.com/ConsolePro/RemnaManager/internal/updater"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

func (m Model) body() string {
	width := m.contentWidth()
	inner := max(1, width-4)
	switch m.screen {
	case "menu":
		f := m.frames[len(m.frames)-1]
		rows := []string{}
		for i, item := range f.items {
			detail := ""
			if len(item.children) > 0 {
				detail = "›"
			} else if item.action == "pending" {
				detail = m.l("В разработке", "In development")
			} else if item.action == "install" {
				detail = "DEMO"
			}
			rows = append(rows, menuRow(menuIcon(item), m.l(item.ru, item.en), detail, i == f.cursor, inner))
			if m.roomyMenu() && i < len(f.items)-1 {
				rows = append(rows, strings.Repeat(" ", inner))
			}
		}
		result := panel(strings.Join(rows, "\n"), width)
		if len(m.frames) > 1 {
			result = muted.Render(ansi.Truncate(m.title(), width, "…")) + "\n" + result
		}
		if m.updateStatus != "" {
			result += "\n" + muted.Render(m.updateStatus)
		}
		return result
	case "language":
		rows := []string{textStyle.Bold(true).Render("Язык / Language"), muted.Render("Выберите язык интерфейса / Choose your language"), ""}
		for i, s := range []string{"Русский", "English"} {
			rows = append(rows, optionRow(s, "", i == m.language, inner))
		}
		return panel(strings.Join(rows, "\n"), width)
	case "email":
		status := m.l("Enter — сохранить настройки", "Enter — save settings")
		if m.saving {
			status = m.l("Сохранение…", "Saving…")
		}
		return panel(textStyle.Bold(true).Render(m.l("Email администратора", "Administrator email"))+"\n"+muted.Render(m.l("Для выпуска и продления сертификатов ACME", "For ACME certificate issuance and renewal"))+"\n\n"+m.inputView(0)+"\n\n"+muted.Render(status), width)
	case "environment":
		status := m.environment
		if m.checking {
			status = m.l("Проверка окружения…", "Checking environment…")
		}
		return panel(lineBetween(textStyle.Bold(true).Render(m.l("Установка панели", "Panel installation")), badge("DEMO"), inner)+"\n\n"+status+"\n\n"+muted.Render(m.l("Все этапы фиктивные. Сервисы и файлы не изменяются.", "All stages are simulated. Services and files are not changed."))+"\n\n"+accent.Render(m.l("Enter — продолжить", "Enter — continue")), width)
	case "domains":
		return panel(lineBetween(textStyle.Bold(true).Render(m.l("Домены", "Domains")), badge("DEMO"), inner)+"\n\n"+muted.Render(m.l("Домен панели", "Panel domain"))+"\n"+m.inputView(0)+"\n\n"+muted.Render(m.l("Домен подписки", "Subscription domain"))+"\n"+m.inputView(1), width)
	case "confirm":
		p := m.parameters()
		return panel(textStyle.Bold(true).Render(m.l("Всё готово к демонстрации", "Ready for demonstration"))+fmt.Sprintf("\n\n%s  %s\n%s  %s\nEmail  %s\n\n", m.l("Панель", "Panel"), p.PanelDomain, m.l("Подписка", "Subscription"), p.SubscriptionDomain, m.cfg.Email)+muted.Render(m.l("Реальная установка не выполняется.", "No actual installation will be performed."))+"\n\n"+accent.Render(m.l("Enter — запустить демо", "Enter — run demo")), width)
	case "progress":
		return m.progressView()
	case "notice":
		return panel(m.notice+"\n\n"+muted.Render(m.l("Enter / Esc — в меню", "Enter / Esc — return to menu")), width)
	}
	return ""
}

func (m Model) roomyMenu() bool { return m.height >= 32 && m.contentWidth() >= 60 }

func (m Model) progressView() string {
	steps := m.service.Stages()
	width := m.contentWidth()
	inner := max(1, width-4)
	title := textStyle.Bold(true).Render(m.l("Установка панели Remnawave", "Remnawave panel installation"))
	rows := []string{lineBetween(title, badge("DEMO"), inner), ""}
	for i, step := range steps {
		label := m.l(step[0], step[1])
		row := muted.Render("○  " + label)
		if i < m.stage {
			row = accent.Render("✓") + "  " + textStyle.Render(label)
		}
		if i == m.stage {
			row = warning.Render("◉") + "  " + textStyle.Bold(true).Render(label)
		}
		rows = append(rows, row)
		if m.height >= 44 {
			rows = append(rows, "")
		}
	}
	fraction := float64(m.stage) / float64(len(steps))
	bar := m.progress
	bar.Width = inner
	rows = append(rows, "", bar.ViewAs(fraction), lineBetween(m.l("Шаг", "Step")+fmt.Sprintf(" %d / %d", min(m.stage+1, len(steps)), len(steps)), fmt.Sprintf("%.0f%%", fraction*100), inner))
	status := m.l("Демонстрация. Все операции фиктивные.", "Demonstration. All operations are simulated.")
	if m.stage == len(steps) {
		status = m.l("Демонстрация завершена. Enter — в меню.", "Demonstration complete. Enter — return to menu.")
	}
	if m.problem != "" {
		status = m.problem
	}
	rows = append(rows, accent.Render(status))
	if len(m.logs) > 0 {
		rows = append(rows, "", muted.Render(m.l("Журнал демонстрации", "Demonstration log")))
		for _, entry := range m.logs {
			rows = append(rows, muted.Render("  "+entry))
		}
	}
	return panel(strings.Join(rows, "\n"), width)
}

func (m Model) headerView() string {
	width := m.contentWidth()
	compact := width < 70 || m.height < 26
	logo := terminalLogo(compact)
	name := textStyle.Bold(true).Render("REMNA MANAGER")
	version := lipgloss.NewStyle().Foreground(lipgloss.Color("#B3C7CE")).Bold(true).Render(updater.Version)
	brandWidth := max(1, width-lipgloss.Width(logo)-4)
	title := lineBetween(name, version, brandWidth)
	tagline := muted.Render(m.l("Управляй инфраструктурой легко", "Manage your infrastructure with ease"))
	detail := ""
	if updater.Newer(m.latest, updater.Version) {
		detail = accent.Render(m.l("Доступна ", "Available ")+m.latest) + muted.Render(" · "+m.l("Обновление: в разработке", "Update: in development"))
	}
	brand := []string{title, ansi.Truncate(tagline, brandWidth, "…")}
	if !compact || detail != "" {
		brand = append(brand, ansi.Truncate(detail, brandWidth, "…"))
	}
	separator := lipgloss.NewStyle().Foreground(lipgloss.Color("#49646A")).Render(strings.TrimSuffix(strings.Repeat("│\n", len(brand)), "\n"))
	return lipgloss.JoinHorizontal(lipgloss.Top, logo, "  ", separator, " ", strings.Join(brand, "\n"))
}

func (m Model) View() string {
	if m.width < 36 || m.height < 18 {
		return m.l("Увеличьте терминал до 36×18.\nF1 — выход, Ctrl+C — выход.", "Resize terminal to 36×18.\nF1 — exit, Ctrl+C — exit.") + "\n" + m.exitView()
	}
	width := m.contentWidth()
	divider := muted.Foreground(lipgloss.Color(edge)).Render(strings.Repeat("─", width))
	roomy := m.roomyMenu()
	topParts := []string{m.headerView(), divider}
	if roomy {
		topParts = append(topParts, "")
	}
	topParts = append(topParts, m.referenceStatsView())
	if network := m.networkStatusView(); network != "" {
		topParts = append(topParts, network)
	}
	if roomy {
		topParts = append(topParts, "")
	}
	topParts = append(topParts, divider, "")
	top := strings.Join(topParts, "\n") + "\n"
	hint, action := m.footerView()
	footerLine := lineBetween(keyHints(hint), enterButton(action), width)
	if m.exitConfirm {
		footerLine = m.exitView()
	}
	footer := divider + "\n" + footerLine
	if roomy {
		footer = "\n" + footer
	}
	verticalPadding := 0
	if m.height >= 32 {
		verticalPadding = 1
	}
	available := max(1, m.height-2-verticalPadding*2-lipgloss.Height(top)-lipgloss.Height(footer))
	body := m.body()
	if m.problem != "" && m.screen != "progress" {
		body += "\n" + failure.Render(m.problem)
	}
	lines := strings.Split(body, "\n")
	for i, line := range lines {
		lines[i] = ansi.Truncate(line, width, "…")
	}
	vp := m.viewport
	vp.Width = width
	vp.Height = min(available, len(lines))
	vp.SetContent(strings.Join(lines, "\n"))
	vp.SetYOffset(m.scroll)
	if m.screen == "menu" {
		target := m.frames[len(m.frames)-1].cursor
		if roomy {
			target *= 2
		}
		target++ // Rounded panel's top border.
		if len(m.frames) > 1 {
			target++
		}
		if target >= vp.YOffset+vp.Height {
			vp.SetYOffset(target - vp.Height + 1)
		}
		if target < vp.YOffset {
			vp.SetYOffset(target)
		}
	} else if m.problem != "" {
		vp.GotoBottom()
	} else if m.screen == "domains" && m.focus == 1 && available < 8 {
		vp.SetYOffset(8 - available)
	} else if m.screen == "language" && m.language == 1 && available < 6 {
		vp.SetYOffset(6 - available)
	} else if m.screen == "email" && available < 5 {
		vp.SetYOffset(5 - available)
	} else if m.screen == "progress" && m.scroll == 0 && available < 15 {
		steps := m.service.Stages()
		status := m.l("Готово · Enter — в меню", "Complete · Enter — menu")
		if m.stage < len(steps) {
			status = m.l(steps[m.stage][0], steps[m.stage][1])
		}
		bar := m.progress
		bar.Width = max(1, width-4)
		vp.SetContent(panel(accent.Render(status)+"\n"+bar.ViewAs(float64(m.stage)/float64(len(steps)))+fmt.Sprintf("\n%d / %d · %.0f%%", m.stage, len(steps), 100*float64(m.stage)/float64(len(steps))), width))
		vp.Height = min(available, 5)
		vp.GotoTop()
	}
	content := top + vp.View() + "\n" + footer
	lines = strings.Split(content, "\n")
	for i, line := range lines {
		lines[i] = ansi.Truncate(line, width, "…")
	}
	frame := textStyle.Background(lipgloss.Color(background)).Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color(windowEdge)).Padding(verticalPadding, m.horizontalPadding()).Width(min(124, m.width) - 2).Render(inheritBackground(strings.Join(lines, "\n"), background))
	return lipgloss.PlaceHorizontal(m.width, lipgloss.Center, frame)
}

func (m Model) footerView() (string, string) {
	hint := m.l("↑↓ Выбор · Enter Открыть · Esc Назад · F1 Выход", "↑↓ Select · Enter Open · Esc Back · F1 Exit")
	action := "Enter ↵"
	if m.screen == "email" || m.screen == "domains" {
		hint = m.l("Tab Поле · Enter Далее · Esc Назад · F1 Выход", "Tab Field · Enter Next · Esc Back · F1 Exit")
	}
	if m.screen == "progress" {
		hint = m.l("PgUp/PgDn Журнал · Esc Назад · F1 Выход", "PgUp/PgDn Log · Esc Back · F1 Exit")
		action = "DEMO"
	}
	return hint, action
}
func (m Model) exitView() string {
	if !m.exitConfirm {
		return ""
	}
	no, yes := m.l("Нет", "No"), m.l("Да", "Yes")
	if m.exitYes {
		yes = enterButton(yes)
	} else {
		no = enterButton(no)
	}
	return textStyle.Bold(true).Render(m.l("Выйти?", "Exit?")) + "  " + no + " / " + yes + "  ← → Enter · Esc"
}
