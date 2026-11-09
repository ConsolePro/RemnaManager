package ui

import (
	"github.com/charmbracelet/lipgloss"
	"strings"
)

// The reference uses a typographic RM monogram. Block cells reproduce its
// silhouette in ordinary SSH terminals, including monochrome/256-color sessions.
func terminalLogo(compact bool) string {
	if compact {
		return accent.Italic(true).Render("R") + lipgloss.NewStyle().Foreground(lipgloss.Color("#69E7B5")).Bold(true).Italic(true).Render("M")
	}
	r := []string{"111110", "110011", "111110", "111100", "110110", "110011"}
	m := []string{"1100011", "1110111", "1111111", "1101011", "1100011", "1100011"}
	draw := func(rows []string, color string) []string {
		result := make([]string, 3)
		style := lipgloss.NewStyle().Foreground(lipgloss.Color(color))
		for y := 0; y < 3; y++ {
			var line strings.Builder
			for x := range rows[0] {
				top, bottom := rows[y*2][x] == '1', rows[y*2+1][x] == '1'
				glyph := " "
				switch {
				case top && bottom:
					glyph = "█"
				case top:
					glyph = "▀"
				case bottom:
					glyph = "▄"
				}
				line.WriteString(glyph)
			}
			result[y] = style.Render(line.String())
		}
		return result
	}
	left, right := draw(r, "#38D998"), draw(m, "#69E7B5")
	lines := make([]string, 3)
	for y := range lines {
		lines[y] = strings.Repeat(" ", 2-y) + left[y] + " " + right[y] + strings.Repeat(" ", y)
	}
	return strings.Join(lines, "\n")
}
