package ui

import (
	"fmt"
	"strings"

	"github.com/ConsolePro/RemnaManager/internal/system/stats"
	"github.com/charmbracelet/bubbles/progress"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

type serverCard struct {
	title, value string
	lines        []string
}

func (m Model) classicStatsView() string {
	if m.lastStats.IsZero() {
		return muted.Render(m.l("Получаем текущие данные сервера…", "Reading current server metrics…"))
	}
	s := m.stats
	used := uint64(0)
	if s.Memory > s.Available {
		used = s.Memory - s.Available
	}
	ramPercent := "—"
	if s.Memory > 0 {
		ramPercent = fmt.Sprintf("%.0f%%", 100*float64(used)/float64(s.Memory))
	}
	cores := fmt.Sprintf("%d %s", s.CPUs, m.l(stats.Cores(s.CPUs), "cores"))
	model := strings.Join(strings.Fields(s.CPUModel), " ")
	model = strings.NewReplacer("(R)", "", "(TM)", "", " CPU", "", " Processor", "").Replace(model)
	if at := strings.Index(model, " @ "); at >= 0 {
		model = model[:at]
	}
	model = strings.Join(strings.Fields(model), " ")
	if model == "" {
		model = m.l("Модель: —", "Model: —")
	}
	v4, v6 := s.IPv4, s.IPv6
	if v4 == "" {
		v4 = "Not connected"
	}
	if v6 == "" {
		v6 = "Not connected"
	}
	if v6 == "Not connected" {
		v6 = m.l("IPv6 не подключён", "IPv6 disconnected")
	} else {
		v6 = "IPv6 " + v6
	}
	cards := []serverCard{
		{m.l("ЦП", "CPU"), fmt.Sprintf("%.0f%%", s.CPU), []string{cores, model}},
		{m.l("ОЗУ", "RAM"), ramPercent, []string{m.volumeLine(used, s.Memory), fmt.Sprintf("SWAP: %.1f GB", gb(s.Swap))}},
		{m.l("Диск", "Disk"), fmt.Sprintf("%.0f%%", s.DiskPercent), []string{m.volumeLine(s.DiskUsed, s.Disk), fmt.Sprintf(m.l("Своб. %.1f GB", "Free %.1f GB"), gb(s.Free))}},
		{m.l("Сеть", "Network"), "IPv4", []string{v4, v6}},
	}
	width := m.contentWidth()
	if width < 66 || m.height < 24 {
		return compactStats(cards, width)
	}
	panelColor := lipgloss.CompleteColor{TrueColor: "#151826", ANSI256: "234", ANSI: "0"}
	cardColor := lipgloss.CompleteColor{TrueColor: "#1C2633", ANSI256: "235", ANSI: "0"}
	label := lipgloss.NewStyle().Foreground(lipgloss.Color("250")).Background(cardColor)
	icon := lipgloss.NewStyle().Foreground(lipgloss.Color("42")).Background(cardColor)
	value := lipgloss.NewStyle().Foreground(lipgloss.Color("195")).Background(cardColor).Bold(true)
	detail := lipgloss.NewStyle().Foreground(lipgloss.Color("245")).Background(cardColor)
	primary := detail.Foreground(lipgloss.Color("250"))
	icons := []string{"⊞", "▤", "▱", "⌘"}
	available := width - 4 // Outer border and padding.
	base := (available - 3) / 4
	widths := []int{base, base, base, available - 3 - base*3}
	contents := make([]string, 4)
	rows := make([][]string, 4)
	maxRows := 5
	for i, card := range cards {
		inner := max(1, widths[i]-4)
		rows[i] = []string{icon.Render(icons[i]) + " " + label.Render(card.title), "", value.Render(card.value)}
		for j, line := range card.lines {
			style := detail
			if j == 0 {
				style = primary
			}
			if i == 0 && j == 1 || i == 3 && j == 1 {
				wrapped := strings.Split(ansi.Hardwrap(line, inner, true), "\n")
				if i == 0 && len(wrapped) > 2 {
					wrapped = wrapped[:2]
					wrapped[1] = ansi.Truncate(wrapped[1], max(1, inner-1), "…")
				}
				for _, part := range wrapped {
					rows[i] = append(rows[i], style.Render(part))
				}
			} else {
				if j == 0 && (i == 1 || i == 2) && lipgloss.Width(line) > inner {
					line = strings.ReplaceAll(line, " / ", "/")
				}
				rows[i] = append(rows[i], style.Render(ansi.Truncate(line, inner, "…")))
			}
		}
		maxRows = max(maxRows, len(rows[i]))
	}
	for i := range cards {
		// Half-cell edges give filled cards soft corners without image protocols.
		border := lipgloss.Border{Top: "▄", Bottom: "▀", Left: " ", Right: " ", TopLeft: "▗", TopRight: "▖", BottomLeft: "▝", BottomRight: "▘"}
		contents[i] = lipgloss.NewStyle().Background(cardColor).Border(border).BorderForeground(cardColor).BorderBackground(panelColor).BorderLeftBackground(cardColor).BorderRightBackground(cardColor).Width(widths[i]-2).Height(maxRows).Padding(0, 1).Render(strings.Join(rows[i], "\n"))
	}
	gap := lipgloss.NewStyle().Background(panelColor).Height(maxRows + 2).Render(" ")
	panel := lipgloss.JoinHorizontal(lipgloss.Top, contents[0], gap, contents[1], gap, contents[2], gap, contents[3])
	return lipgloss.NewStyle().Background(panelColor).Border(lipgloss.RoundedBorder()).BorderForeground(panelColor).Padding(0, 1).Render(panel)
}

func (m Model) volumeLine(used, total uint64) string {
	return fmt.Sprintf("%.1f / %.1f GB", gb(used), gb(total))
}

func compactStats(cards []serverCard, width int) string {
	cell := (width - 3) / 2
	rows := make([]string, 2)
	for group := 0; group < 2; group++ {
		parts := []string{}
		for i := 0; i < 2; i++ {
			card := cards[group*2+i]
			title := card.title + " " + card.value
			detail := card.lines[0]
			parts = append(parts, lipgloss.NewStyle().Width(cell).Render(accent.Render(ansi.Truncate(title, cell, "…"))+"\n"+muted.Render(ansi.Truncate(detail, cell, "…"))))
		}
		rows[group] = lipgloss.JoinHorizontal(lipgloss.Top, parts[0], " │ ", parts[1])
	}
	return strings.Join(rows, "\n")
}

func (m Model) classicProgressView(fraction float64) string {
	bar := progress.New(progress.WithDefaultGradient(), progress.WithColorProfile(lipgloss.ColorProfile()))
	bar.Width = m.progress.Width
	return bar.ViewAs(fraction)
}
