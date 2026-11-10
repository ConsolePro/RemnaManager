package ui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// Two-row numerals are smaller than the previous three-row display. Narrow
// cards use ordinary one-line percentages instead of clipped large glyphs.
func largePercentage(value string, width int) []string {
	font := map[rune][2]string{
		'0': {"╭╮", "╰╯"}, '1': {"╷", "╵"},
		'2': {"╶┐", "└╴"}, '3': {"╶┤", "╶┘"},
		'4': {"└┤", " ╵"}, '5': {"┌╴", "╶┘"},
		'6': {"┌╴", "└┘"}, '7': {"╶┐", " ╵"},
		'8': {"┌┐", "└┘"}, '9': {"└┤", "╶┘"},
	}
	digits := strings.TrimSuffix(value, "%")
	if width < 18 {
		return []string{accent.Render(value)}
	}
	if len(digits) == 0 || !strings.HasSuffix(value, "%") {
		return []string{accent.Render(value)}
	}
	rows := make([]string, 2)
	for row := 0; row < 2; row++ {
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
