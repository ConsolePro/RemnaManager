package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/ConsolePro/RemnaManager/internal/system/stats"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

type metricView struct{ icon, label, value, detail, compact string }

func metricGB(n uint64) string { return strings.TrimSuffix(fmt.Sprintf("%.1f", gb(n)), ".0") }

// Presentation only: all numbers come from the existing snapshot. In particular,
// that snapshot has no SWAP usage; a dash is more honest than an invented zero.
func (m Model) referenceStatsView() string {
	width := m.contentWidth()
	if m.lastStats.IsZero() {
		return muted.Render(m.l("Получаем текущие данные сервера…", "Reading current server metrics…"))
	}
	s := m.stats
	usedRAM := uint64(0)
	if s.Memory > s.Available {
		usedRAM = s.Memory - s.Available
	}
	usedDisk := uint64(0)
	if s.Disk > s.Free {
		usedDisk = s.Disk - s.Free
	}
	ramPercent := "—"
	if s.Memory > 0 {
		ramPercent = fmt.Sprintf("%.0f%%", 100*float64(usedRAM)/float64(s.Memory))
	}
	cells := []metricView{
		{"▣", m.l("ЦП", "CPU"), fmt.Sprintf("%.0f%%", s.CPU), fmt.Sprintf("%d %s", s.CPUs, m.l(stats.Cores(s.CPUs), "cores")), ""},
		{"▤", m.l("ОЗУ", "RAM"), ramPercent, fmt.Sprintf("%s / %s GB", metricGB(usedRAM), metricGB(s.Memory)), fmt.Sprintf("%s/%sG", metricGB(usedRAM), metricGB(s.Memory))},
		{"▱", "SWAP", "—", fmt.Sprintf("— / %s GB", metricGB(s.Swap)), fmt.Sprintf("—/%sG", metricGB(s.Swap))},
		{"▰", m.l("Диск", "Disk"), fmt.Sprintf("%.0f%%", s.DiskPercent), fmt.Sprintf("%s / %s GB", metricGB(usedDisk), metricGB(s.Disk)), fmt.Sprintf("%s/%sG", metricGB(usedDisk), metricGB(s.Disk))},
	}
	columns := 2
	if width >= 108 {
		columns = 4
	}
	cellWidth := (width - 3*(columns-1)) / columns
	rows := []string{}
	for start := 0; start < len(cells); start += columns {
		parts := []string{}
		for offset := 0; offset < columns; offset++ {
			cell := cells[start+offset]
			heading := accent.Render(cell.icon) + " " + textStyle.Bold(true).Render(cell.label)
			value := accent.Render(cell.value)
			line := heading + " " + value + muted.Render(" · "+cell.detail)
			if start+offset == 0 {
				line = heading + muted.Render(" "+cell.detail+" · ") + value
			}
			if lipgloss.Width(line) > cellWidth && cell.compact != "" {
				line = heading + " " + value + muted.Render(" · "+cell.compact)
			}
			if lipgloss.Width(line) > cellWidth {
				line = heading + " " + value + "\n" + muted.Render(cell.detail)
			}
			lines := strings.Split(line, "\n")
			for i := range lines {
				lines[i] = ansi.Truncate(lines[i], cellWidth, "…")
			}
			parts = append(parts, textStyle.Width(cellWidth).Render(strings.Join(lines, "\n")))
		}
		height := 1
		for _, part := range parts {
			height = max(height, lipgloss.Height(part))
		}
		joined := []string{}
		for i, part := range parts {
			if i > 0 {
				joined = append(joined, muted.Foreground(lipgloss.Color(edge)).Render(strings.TrimSuffix(strings.Repeat(" │ \n", height), "\n")))
			}
			joined = append(joined, lipgloss.NewStyle().Height(height).Render(part))
		}
		rows = append(rows, lipgloss.JoinHorizontal(lipgloss.Top, joined...))
	}
	return strings.Join(rows, "\n")
}

func (m Model) networkStatusView() string {
	if m.lastStats.IsZero() {
		return ""
	}
	status := accent.Render("● LIVE") + muted.Render(" "+m.lastStats.Format("15:04:05"))
	if time.Since(m.lastStats) > 5*time.Second {
		status = warning.Render(m.l("● Метрики устарели", "● Metrics delayed"))
	}
	if m.stats.Err != nil {
		status = warning.Render(m.l("● Частичные данные", "● Partial data"))
	}
	network := muted.Render(fmt.Sprintf("IPv4 %s · IPv6 %s", m.stats.IPv4, m.stats.IPv6))
	width := m.contentWidth()
	if lipgloss.Width(network)+lipgloss.Width(status)+2 > width {
		remaining := max(0, width-lipgloss.Width(status)-2)
		network = ansi.Truncate(network, remaining, "…")
	}
	return lineBetween(network, status, width)
}
