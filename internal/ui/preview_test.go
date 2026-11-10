package ui

import (
	"github.com/ConsolePro/RemnaManager/internal/services"
	"github.com/ConsolePro/RemnaManager/internal/system/stats"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRenderPreviewArtifacts(t *testing.T) {
	dir := os.Getenv("REMNA_PREVIEW_DIR")
	if dir == "" {
		t.Skip("local visual review")
	}
	t.Setenv("NO_COLOR", "")
	previous := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(previous)
	m := testModel(t, true)
	m.width = 124
	m.height = 36
	m.frames[0].cursor = 3
	m.lastStats = time.Now()
	m.stats = stats.Snapshot{CPUs: 2, CPU: 10, CPUModel: "Intel Xeon E5-2680 v4", Uptime: 12*86400 + 3*3600 + 21*60, Memory: 4080218931, Available: 3006477107, Swap: 1 << 29, Disk: 33715493273, DiskUsed: 10522669875, Free: 22011707392, DiskPercent: 31, IPv4: "192.0.2.10", IPv6: "Not connected"}
	if err := os.WriteFile(filepath.Join(dir, "menu.ansi"), []byte(m.View()), 0600); err != nil {
		t.Fatal(err)
	}
	lipgloss.SetColorProfile(termenv.ANSI256)
	if err := os.WriteFile(filepath.Join(dir, "menu-256.ansi"), []byte(m.View()), 0600); err != nil {
		t.Fatal(err)
	}
	m.width, m.height = 80, 30
	if err := os.WriteFile(filepath.Join(dir, "menu-narrow.ansi"), []byte(m.View()), 0600); err != nil {
		t.Fatal(err)
	}
	m.width, m.height = 124, 36
	lipgloss.SetColorProfile(termenv.TrueColor)
	m.screen = "progress"
	m.stage = 4
	m.height = 36
	for _, step := range m.service.Stages()[:4] {
		m.logs = append(m.logs, "Демо: "+step[0])
	}
	if err := os.WriteFile(filepath.Join(dir, "progress.ansi"), []byte(m.View()), 0600); err != nil {
		t.Fatal(err)
	}
	m.screen = "panelConfirm"
	m.panelTitle = "Обновить панель"
	m.panelRequest = services.PanelRequest{Action: "panel-update", Value: "3.4.10"}
	if err := os.WriteFile(filepath.Join(dir, "management.ansi"), []byte(m.View()), 0600); err != nil {
		t.Fatal(err)
	}
}
