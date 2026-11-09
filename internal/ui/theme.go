package ui

import (
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"strings"
)

const (
	background         = "#0F181F"
	surface            = "#19282F"
	selectedBackground = "#245E56"
	emerald            = "#42C79A"
	foreground         = "#E2E8EB"
	secondary          = "#8D9FA7"
	edge               = "#26373F"
)

var accent = lipgloss.NewStyle().Foreground(lipgloss.Color(emerald)).Bold(true)
var muted = lipgloss.NewStyle().Foreground(lipgloss.Color(secondary))
var warning = lipgloss.NewStyle().Foreground(lipgloss.Color("#E6A34B"))
var failure = lipgloss.NewStyle().Foreground(lipgloss.Color("#F17D84"))
var textStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(foreground))

func (m Model) contentWidth() int { return max(1, min(112, m.width)-6) }

func lineBetween(left, right string, width int) string {
	if lipgloss.Width(left)+lipgloss.Width(right)+2 > width {
		return ansi.Truncate(left, width, "…")
	}
	return left + strings.Repeat(" ", width-lipgloss.Width(left)-lipgloss.Width(right)) + right
}

func badge(label string) string {
	return lipgloss.NewStyle().Foreground(lipgloss.Color(emerald)).Background(lipgloss.Color("#163E32")).Padding(0, 1).Bold(true).Render(label)
}

func optionRow(label, detail string, selected bool, width int) string {
	marker := "● "
	color := surface
	if selected {
		marker = "› "
		color = selectedBackground
	}
	style := textStyle.Background(lipgloss.Color(color)).Width(width).Padding(0, 1)
	if selected {
		style = style.Bold(true)
	}
	row := lineBetween(marker+label, detail, max(1, width-2))
	return style.Render(ansi.Truncate(row, max(1, width-2), "…"))
}

func panel(content string, width int) string {
	return textStyle.Background(lipgloss.Color(surface)).Width(width).Padding(1, 1).Render(inheritBackground(content, surface))
}

// Nested ANSI styles reset colors; restore the surrounding card after resets.
func inheritBackground(content, color string) string {
	sample := textStyle.Background(lipgloss.Color(color)).Render(" ")
	if i := strings.IndexByte(sample, ' '); i > 0 {
		return strings.ReplaceAll(content, "\x1b[0m", "\x1b[0m"+sample[:i])
	}
	return content
}
