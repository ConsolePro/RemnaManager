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
		v6 = "IPv6 —"
	} else {
		v6 = "IPv6 " + v6
	}
	cards := []serverCard{
		{m.l("ЦП", "CPU"), fmt.Sprintf("%.0f%%", s.CPU), []string{cores, model}},
		{m.l("ОЗУ", "RAM"), ramPercent, []string{m.volumeLine(used, s.Memory), fmt.Sprintf("SWAP: %.1f GB", gb(s.Swap))}},
		{m.l("Диск", "Disk"), fmt.Sprintf("%.0f%%", s.DiskPercent), []string{m.volumeLine(s.DiskUsed, s.Disk), fmt.Sprintf(m.l("Своб. %.1f GB", "Free %.1f GB"), gb(s.Free))}},
		{m.l("Сеть", "Network"), "IPv4 " + v4, []string{v6}},
	}
	width := m.contentWidth()
	if width < 66 || m.height < 24 {
		return compactStats(cards, width)
	}
	label := lipgloss.NewStyle().Foreground(lipgloss.Color("247"))
	value := lipgloss.NewStyle().Foreground(lipgloss.Color("255")).Bold(true)
	network := value
	detail := lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	primary := detail.Foreground(lipgloss.Color("250"))
	available := width
	base := (available - 3) / 4
	// Reserve enough space for the IPv4 label and address on the same line.
	networkWidth := min(available-3-3*12, max(base, lipgloss.Width(cards[3].value)+4))
	base = (available - 3 - networkWidth) / 3
	widths := []int{base, base, available - 3 - networkWidth - base*2, networkWidth}
	contents := make([]string, 4)
	rows := make([][]string, 4)
	maxRows := 5
	for i, card := range cards {
		inner := max(1, widths[i]-4)
		valueStyle := value
		if i == 3 {
			valueStyle = network
		}
		rows[i] = []string{label.Render(card.title), "", valueStyle.Render(card.value)}
		for j, line := range card.lines {
			style := detail
			if j == 0 {
				style = primary
			}
			if i == 3 {
				style = network
			}
			if i == 0 && j == 1 || i == 3 {
				wrappedLine := ansi.Hardwrap(line, inner, true)
				if i == 0 {
					wrappedLine = ansi.Wrap(line, inner, "")
				}
				wrapped := strings.Split(wrappedLine, "\n")
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
		// Use the terminal's background throughout: no rectangular fill can
		// protrude beyond the rounded frame or clash with an SSH color theme.
		borderColor := lipgloss.CompleteColor{TrueColor: "#354052", ANSI256: "237", ANSI: "8"}
		contents[i] = lipgloss.NewStyle().Foreground(lipgloss.Color("250")).Border(lipgloss.RoundedBorder()).BorderForeground(borderColor).Width(widths[i]-2).Height(maxRows).Padding(0, 1).Render(strings.Join(rows[i], "\n"))
	}
	return lipgloss.JoinHorizontal(lipgloss.Top, contents[0], " ", contents[1], " ", contents[2], " ", contents[3])
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
			if group*2+i == 3 {
				title, detail = card.title, card.value
			}
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
