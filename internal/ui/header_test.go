package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/ConsolePro/RemnaManager/internal/system/stats"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
)

func TestServerTimeAndUptime(t *testing.T) {
	m := testModel(t, true)
	m.width, m.height = 110, 30
	now := time.Date(2026, 11, 10, 5, 42, 48, 0, time.FixedZone("YEKT", 5*3600))
	m.lastStats = now.Add(-2 * time.Second)
	m.stats.Uptime = 90060
	header := ansi.Strip(m.serverHeader(now))
	for _, want := range []string{"REMNA MANAGER", "10.11.2026 05:42:48 YEKT", "Аптайм: 1 д 01:01:02", "v1.0.0", "Remnawave"} {
		if !strings.Contains(header, want) {
			t.Fatalf("missing %q: %s", want, header)
		}
	}
	if strings.Contains(header, "LIVE") || strings.Contains(header, "█") {
		t.Fatal("header must use readable text without LIVE", header)
	}
	later := ansi.Strip(m.serverHeader(now.Add(time.Second)))
	if !strings.Contains(later, "05:42:49") || !strings.Contains(later, "01:01:03") {
		t.Fatal("clock and server uptime did not advance", later)
	}
	if strings.Index(header, "v1.0.0") < strings.Index(header, "Аптайм") {
		t.Fatal("version must appear below server time and uptime")
	}
}

func TestUptimeFormatting(t *testing.T) {
	for n, want := range map[uint64]string{0: "00:00:00", 59: "00:00:59", 3600: "01:00:00", 86400: "1 д 00:00:00", 90061: "1 д 01:01:01"} {
		if got := formatUptime(n, false); got != want {
			t.Fatal(n, got, want)
		}
	}
	if formatUptime(172800, true) != "2 d 00:00:00" {
		t.Fatal("English uptime format")
	}
}

func TestStatsCardsAndLongIPv6(t *testing.T) {
	m := testModel(t, true)
	m.width, m.height = 80, 30
	m.lastStats = time.Now()
	m.stats = stats.Snapshot{CPUs: 2, CPU: 10, CPUModel: "Intel Xeon", Memory: 4 << 30, Available: 3 << 30, Swap: 1 << 29, Disk: 32 << 30, DiskUsed: 10 << 30, Free: 21 << 30, DiskPercent: 31, IPv4: "89.144.34.16", IPv6: "2001:db8:1234:5678:9abc:def0:1234:abcd", Uptime: 86401}
	block := ansi.Strip(m.classicStatsView())
	for _, want := range []string{"ЦП", "2 ядра", "Intel Xeon", "ОЗУ", "1.0 / 4.0 GB", "SWAP: 0.5 GB", "Диск", "IPv4 89.144.34.16", "2001:db8", ":abcd"} {
		if !strings.Contains(block, want) {
			t.Fatalf("missing %q: %s", want, block)
		}
	}
	inline := false
	for _, line := range strings.Split(block, "\n") {
		if strings.Contains(line, "ЦП") && strings.Contains(line, "ОЗУ") && strings.Contains(line, "Диск") && strings.Contains(line, "Сеть") {
			inline = true
		}
	}
	if !inline {
		t.Fatal("cards should be side by side")
	}
	for _, size := range [][2]int{{36, 18}, {60, 24}, {80, 24}, {80, 30}, {124, 36}} {
		m.width, m.height = size[0], size[1]
		view := m.View()
		if lipgloss.Height(view) > m.height {
			t.Fatal("header displaced footer", size, lipgloss.Height(view))
		}
		for _, line := range strings.Split(view, "\n") {
			if lipgloss.Width(line) > m.width {
				t.Fatal("cards overflowed terminal", size)
			}
		}
	}
}

func TestIPv4StyleRemainsStableAcrossMetricUpdates(t *testing.T) {
	previous := lipgloss.ColorProfile()
	defer lipgloss.SetColorProfile(previous)
	for _, profile := range []termenv.Profile{termenv.ANSI, termenv.ANSI256, termenv.TrueColor} {
		lipgloss.SetColorProfile(profile)
		m := testModel(t, true)
		m.width, m.height = 124, 36
		m.lastStats = time.Now()
		m.stats = stats.Snapshot{CPUs: 2, CPU: 10, Memory: 4 << 30, Available: 3 << 30, Disk: 32 << 30, IPv4: "192.0.2.10", IPv6: "2001:db8::1"}
		networkSuffix := func() string {
			for _, line := range strings.Split(m.classicStatsView(), "\n") {
				if at := strings.Index(line, "IPv4 192.0.2.10"); at >= 0 {
					// Include the SGR sequence that sets the network text's style.
					start := strings.LastIndex(line[:at], "\x1b[")
					if start < 0 {
						t.Fatal("missing explicit network style")
					}
					return line[start:]
				}
			}
			t.Fatal("missing IPv4 address")
			return ""
		}
		before := networkSuffix()
		m.stats.CPU = 25
		m.stats.Available = 2 << 30
		if after := networkSuffix(); before != after {
			t.Fatalf("IPv4 style changed during a metrics refresh: %q -> %q", before, after)
		}
	}
}
