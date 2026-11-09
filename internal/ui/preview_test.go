package ui

import (
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
	m.width = 96
	m.height = 36
	m.frames[0].cursor = 3
	m.lastStats = time.Now()
	m.stats = stats.Snapshot{CPUs: 4, CPU: 17, Memory: 4 << 30, Available: 2 << 30, Swap: 2 << 30, Disk: 40 << 30, Free: 32 << 30, DiskPercent: 20, IPv4: "192.0.2.10", IPv6: "Not connected"}
	if err := os.WriteFile(filepath.Join(dir, "menu.ansi"), []byte(m.View()), 0600); err != nil {
		t.Fatal(err)
	}
	m.screen = "progress"
	m.stage = 4
	m.height = 32
	for _, step := range m.service.Stages()[:4] {
		m.logs = append(m.logs, "Демо: "+step[0])
	}
	if err := os.WriteFile(filepath.Join(dir, "progress.ansi"), []byte(m.View()), 0600); err != nil {
		t.Fatal(err)
	}
}
