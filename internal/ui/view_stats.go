package ui

import (
	"fmt"
	"time"

	"github.com/ConsolePro/RemnaManager/internal/system/stats"
	"github.com/charmbracelet/bubbles/progress"
	"github.com/charmbracelet/lipgloss"
)

// Keep the first Go TUI's multiline metrics while preserving live refresh and
// the loading/delayed-data indicators added after that initial implementation.
func (m Model) classicStatsView() string {
	if m.lastStats.IsZero() {
		return m.l("Получаем текущие данные сервера…", "Reading current server metrics…")
	}
	s := m.stats
	text := fmt.Sprintf(m.l("ЦП: %d %s · %.0f%%\nОЗУ: %.1f GB всего · %.1f GB доступно · SWAP %.1f GB\nДиск /: %.1f GB всего · %.1f GB свободно · %.0f%% занято", "CPU: %d %s · %.0f%%\nRAM: %.1f GB total · %.1f GB available · SWAP %.1f GB\nDisk /: %.1f GB total · %.1f GB free · %.0f%% used"), s.CPUs, m.l(stats.Cores(s.CPUs), "cores"), s.CPU, gb(s.Memory), gb(s.Available), gb(s.Swap), gb(s.Disk), gb(s.Free), s.DiskPercent)
	text += fmt.Sprintf("\nIPv4: %s · IPv6: %s", s.IPv4, s.IPv6)
	status := "LIVE " + m.lastStats.Format("15:04:05")
	if time.Since(m.lastStats) > 5*time.Second {
		status = m.l("Метрики устарели", "Metrics delayed")
	}
	if s.Err != nil {
		status = m.l("Часть статистики недоступна.", "Some statistics are unavailable.")
	}
	return text + "\n" + status
}

func (m Model) classicProgressView(fraction float64) string {
	bar := progress.New(progress.WithDefaultGradient(), progress.WithColorProfile(lipgloss.ColorProfile()))
	bar.Width = m.progress.Width
	return bar.ViewAs(fraction)
}
