package ui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// Outline numerals use three terminal rows, keeping percentages legible without
// changing the terminal font or requiring an image protocol.
func largePercentage(value string, width int) []string {
	font := map[rune][3]string{
		'0': {"╭─╮", "│ │", "╰─╯"}, '1': {" ╷ ", " │ ", " ╵ "},
		'2': {"╶─╮", "╭─╯", "╰─╴"}, '3': {"╶─╮", " ─┤", "╶─╯"},
		'4': {"╷ ╷", "╰─┤", "  ╵"}, '5': {"╭─╴", "╰─╮", "╶─╯"},
		'6': {"╭─╴", "├─╮", "╰─╯"}, '7': {"╶─╮", "  │", "  ╵"},
		'8': {"╭─╮", "├─┤", "╰─╯"}, '9': {"╭─╮", "╰─┤", "╶─╯"},
	}
	digits := strings.TrimSuffix(value, "%")
	if len(digits) == 0 || !strings.HasSuffix(value, "%") {
		return []string{accent.Render(value)}
	}
	rows := make([]string, 3)
	for row := 0; row < 3; row++ {
		parts := make([]string, 0, len(digits))
		for _, digit := range digits {
			glyph, ok := font[digit]
			if !ok {
				return []string{accent.Render(value)}
			}
			parts = append(parts, glyph[row])
		}
		suffix := "  "
		if row == 1 {
			suffix = " %"
		}
		rows[row] = strings.Join(parts, " ") + suffix
		if lipgloss.Width(rows[row]) > width {
			return []string{accent.Render(value)}
		}
		rows[row] = accent.Render(rows[row])
	}
	return rows
}
