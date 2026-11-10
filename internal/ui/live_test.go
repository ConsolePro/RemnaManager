package ui

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/ConsolePro/RemnaManager/internal/system/stats"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

func TestStatsPollingAcrossScreens(t *testing.T) {
	for _, screen := range []string{"menu", "email", "progress"} {
		t.Run(screen, func(t *testing.T) {
			m := testModel(t, true)
			m.startSettings(true)
			press(&m, tea.KeyEnter)
			m.inputs[0].SetValue("admin@example.com")
			m.inputs[0].CursorStart()
			m.screen = screen
			m.frames[0].cursor = 2
			m.stage = 3
			reads := 0
			m.statsProvider = func(context.Context) stats.Snapshot {
				reads++
				return stats.Snapshot{CPUs: 4, CPU: float64(reads * 17), Memory: 4 << 30, Available: uint64(reads) << 30, IPv4: "192.0.2.1", IPv6: "Not connected"}
			}
			for i := 1; i <= 3; i++ {
				before := m.classicStatsView()
				next, collect := m.Update(pollStats{})
				m = next.(Model)
				if collect == nil {
					t.Fatal("poll did not collect fresh metrics")
				}
				next, tick := m.Update(collect())
				m = next.(Model)
				if tick == nil {
					t.Fatal("polling stopped")
				}
				if m.stats.CPU != float64(i*17) || m.classicStatsView() == before || strings.Contains(m.View(), "LIVE") {
					t.Fatal("header did not update", m.View())
				}
				if m.screen != screen || m.stage != 3 || m.frames[0].cursor != 2 || m.inputs[0].Value() != "admin@example.com" || m.inputs[0].Position() != 0 {
					t.Fatal("polling changed user state")
				}
				// Execute one real timer to prove the chain schedules another collection.
				if i == 3 {
					if _, ok := tick().(pollStats); !ok {
						t.Fatal("unexpected timer message")
					}
				}
			}
			if reads != 3 {
				t.Fatal(reads)
			}
		})
	}
}

func TestLoadingAndDelayedMetrics(t *testing.T) {
	m := testModel(t, true)
	if strings.Contains(m.View(), "0.0 GB") || strings.Contains(m.View(), "LIVE") {
		t.Fatal("fabricated startup metrics")
	}
	m.lastStats = time.Now().Add(-10 * time.Second)
	if !strings.Contains(m.View(), "Метрики устарели") {
		t.Fatal("missing delayed-metrics status")
	}
}

func TestLayoutAcrossScreens(t *testing.T) {
	previous := lipgloss.ColorProfile()
	defer lipgloss.SetColorProfile(previous)
	for _, profile := range []termenv.Profile{termenv.Ascii, termenv.ANSI, termenv.ANSI256} {
		lipgloss.SetColorProfile(profile)
		t.Run(fmt.Sprintf("profile-%d", profile), checkLayoutAcrossScreens)
	}
}

func checkLayoutAcrossScreens(t *testing.T) {
	for _, size := range [][2]int{{36, 18}, {60, 24}, {80, 24}, {80, 30}, {110, 36}, {160, 50}} {
		for _, screen := range []string{"menu", "language", "email", "domains", "confirm", "progress", "notice", "panelInput", "panelConfirm", "panelProgress", "panelResult", "panelCustom"} {
			m := testModel(t, true)
			m.inputs = append(m.inputs, newInput("", "")) // Allocate both domain inputs.
			m.inputs = append(m.inputs, newInput("", ""))
			m.screen = screen
			m.width, m.height = size[0], size[1]
			m.lastStats = time.Now()
			m.stats = stats.Snapshot{CPUs: 4, Memory: 4 << 30, Available: 2 << 30, IPv4: "192.0.2.10", IPv6: "Not connected"}
			view := m.View()
			if lipgloss.Height(view) > m.height {
				t.Fatalf("%s %v: height %d", screen, size, lipgloss.Height(view))
			}
			for _, line := range strings.Split(view, "\n") {
				if lipgloss.Width(line) > m.width {
					t.Fatalf("%s %v: width %d", screen, size, lipgloss.Width(line))
				}
			}
		}
	}
}
