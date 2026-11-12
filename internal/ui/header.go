package ui

import (
	"fmt"
	"runtime"
	"strings"
	"time"

	"github.com/ConsolePro/RemnaManager/internal/updater"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

func (m Model) serverHeader(now time.Time) string {
	width := m.contentWidth()
	compact := width < 62 || m.height < 24
	title := accent.Render("REMNA MANAGER")
	tagline := m.l("Управляй инфраструктурой Remnawave легко", "Manage your Remnawave infrastructure with ease")
	status := m.metricsStatus(now)
	version := updater.Version + " · Go TUI"
	if updater.Newer(m.latest, updater.Version) {
		version += " · " + m.l("Доступна ", "Available ") + m.latest + " · " + m.l("Версии и обновления", "Versions and updates")
	}
	uptime := "—"
	if !m.lastStats.IsZero() && m.stats.Uptime > 0 {
		elapsed := now.Sub(m.lastStats) / time.Second
		if elapsed < 0 {
			elapsed = 0
		}
		uptime = formatUptime(m.stats.Uptime+uint64(elapsed), m.cfg.Language == "en")
	}
	date := now.Format("02.01.2006 15:04:05 MST")
	right := []string{muted.Render(date), muted.Render(m.l("Аптайм: ", "Uptime: ") + uptime), muted.Render(version)}
	if compact {
		lines := []string{headerLine(title, muted.Render(updater.Version), width), muted.Render(m.serverPlatform(width)), ansi.Truncate(muted.Render(now.Format("02.01 15:04:05"))+" · "+muted.Render(m.l("Аптайм ", "Uptime ")+uptime), width, "…")}
		if status != "" {
			lines = append(lines, ansi.Truncate(status, width, "…"))
		}
		return strings.Join(lines, "\n")
	}
	rightWidth := min(42, max(lipgloss.Width(right[0]), lipgloss.Width(right[1]), lipgloss.Width(right[2])))
	leftWidth := max(1, width-rightWidth-2)
	lines := strings.Split(title, "\n")
	lines = append(lines, muted.Render(ansi.Truncate(tagline, leftWidth, "…")), muted.Render(m.serverPlatform(leftWidth)))
	if status != "" {
		lines = append(lines, status)
	}
	count := max(len(lines), len(right))
	result := make([]string, count)
	for i := 0; i < count; i++ {
		left, r := "", ""
		if i < len(lines) {
			left = ansi.Truncate(lines[i], leftWidth, "…")
		}
		if i < len(right) {
			r = ansi.Truncate(right[i], rightWidth, "…")
		}
		result[i] = headerLine(left, r, width)
	}
	return strings.Join(result, "\n")
}

func (m Model) serverPlatform(width int) string {
	name, version, arch := m.stats.OS, m.stats.OSVersion, m.stats.Arch
	if name == "" {
		name = runtime.GOOS
	}
	if arch == "" {
		arch = runtime.GOARCH
	}
	label := m.l("ОС: ", "OS: ") + name
	if version != "" {
		label += " " + version
	}
	return ansi.Truncate(label, max(0, width-lipgloss.Width(arch)-3), "…") + " · " + arch
}

func headerLine(left, right string, width int) string {
	right = ansi.Truncate(right, width, "…")
	left = ansi.Truncate(left, max(0, width-lipgloss.Width(right)-1), "…")
	return left + strings.Repeat(" ", max(1, width-lipgloss.Width(left)-lipgloss.Width(right))) + right
}

func (m Model) metricsStatus(now time.Time) string {
	if m.lastStats.IsZero() {
		return muted.Render(m.l("Получаем данные сервера…", "Reading server metrics…"))
	}
	if m.stats.Err != nil {
		return warning.Render(m.l("Часть статистики недоступна", "Some statistics are unavailable"))
	}
	if now.Sub(m.lastStats) > 5*time.Second {
		return warning.Render(m.l("Метрики устарели", "Metrics delayed"))
	}
	return ""
}

func formatUptime(seconds uint64, english bool) string {
	days := seconds / 86400
	clock := fmt.Sprintf("%02d:%02d:%02d", seconds/3600%24, seconds/60%60, seconds%60)
	if days == 0 {
		return clock
	}
	label := "д"
	if english {
		label = "d"
	}
	return fmt.Sprintf("%d %s %s", days, label, clock)
}
