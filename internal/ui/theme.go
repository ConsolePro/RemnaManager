package ui

import (
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
	"strings"
)

const (
	background         = "#0D151C"
	surface            = "#14232C"
	selectedBackground = "#206257"
	emerald            = "#39D391"
	foreground         = "#DFECE9"
	secondary          = "#8CA8AB"
	edge               = "#263C42"
	windowEdge         = "#13645D"
)

var accent = lipgloss.NewStyle().Foreground(lipgloss.Color(emerald)).Bold(true)
var muted = lipgloss.NewStyle().Foreground(lipgloss.Color(secondary))
var warning = lipgloss.NewStyle().Foreground(lipgloss.Color("#E6A34B"))
var failure = lipgloss.NewStyle().Foreground(lipgloss.Color("#F17D84"))
var textStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(foreground))

func (m Model) horizontalPadding() int {
	if m.width < 64 {
		return 1
	}
	return 3
}
func (m Model) contentWidth() int { return max(1, min(124, m.width)-2-2*m.horizontalPadding()) }

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
	return menuRow("◇", label, detail, selected, width)
}

func menuRow(icon, label, detail string, selected bool, width int) string {
	color := surface
	if selected {
		color = selectedBackground
	}
	style := textStyle.Background(lipgloss.Color(color)).Width(width).Padding(0, 1)
	if selected {
		style = style.Bold(true)
	}
	prefix := icon + "  "
	if selected && lipgloss.ColorProfile() == termenv.Ascii {
		prefix = icon + "› "
	}
	row := lineBetween(prefix+label, detail, max(1, width-2))
	return style.Render(ansi.Truncate(row, max(1, width-2), "…"))
}

func panel(content string, width int) string {
	return textStyle.Background(lipgloss.Color(surface)).Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color(surface)).Width(max(1, width-2)).Padding(0, 1).Render(inheritBackground(content, surface))
}

func menuIcon(entry item) string {
	switch entry.ru {
	case "Панель и подписка":
		return "◉"
	case "Ноды и расширения":
		return "⬡"
	case "WARP-Native":
		return "◉"
	case "Мониторинг":
		return "▥"
	case "Плагины":
		return "✣"
	case "Балансировщики и мосты":
		return "♧"
	case "Версии и обновления":
		return "↻"
	case "Настройки":
		return "⚙"
	}
	if len(entry.children) > 0 {
		return "▤"
	}
	return "◇"
}

func enterButton(label string) string {
	if lipgloss.ColorProfile() == termenv.Ascii {
		return "[ " + label + " ]"
	}
	return lipgloss.NewStyle().Foreground(lipgloss.Color("#152028")).Background(lipgloss.Color("#F2F4F5")).Bold(true).Padding(0, 2).Render(label)
}

func keyHints(hint string) string {
	parts := strings.Split(hint, " ")
	for i, word := range parts {
		switch word {
		case "Enter", "Esc", "F1", "Tab", "PgUp/PgDn":
			parts[i] = textStyle.Bold(true).Render(word)
		default:
			parts[i] = muted.Render(word)
		}
	}
	return strings.Join(parts, " ")
}

func (m Model) inputView(index int) string {
	// Style a copy; editing state and key handling stay with the original model.
	field := m.inputs[index]
	width := max(1, m.contentWidth()-4)
	field.Width = max(1, min(field.Width, width-2))
	field.TextStyle = textStyle
	field.PlaceholderStyle = muted
	field.PromptStyle = muted
	if index == m.focus {
		field.PromptStyle = accent
	}
	return textStyle.Background(lipgloss.Color(background)).Width(width).Padding(0, 1).Render(inheritBackground(field.View(), background))
}

// Nested ANSI styles reset colors; restore the surrounding card after resets.
func inheritBackground(content, color string) string {
	sample := textStyle.Background(lipgloss.Color(color)).Render(" ")
	if i := strings.IndexByte(sample, ' '); i > 0 {
		return strings.ReplaceAll(content, "\x1b[0m", "\x1b[0m"+sample[:i])
	}
	return content
}
