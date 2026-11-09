package ui

// Colors retained for the existing progress model; rendering uses the original
// Go TUI styles in view.go. Application state and event handling are unchanged.
const emerald = "#00D787"
const edge = "#606060"

func (m Model) contentWidth() int { return max(1, m.width-6) }
