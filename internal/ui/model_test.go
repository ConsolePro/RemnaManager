package ui

import (
	"context"
	"github.com/ConsolePro/RemnaManager/internal/config"
	"github.com/ConsolePro/RemnaManager/internal/installer"
	"github.com/ConsolePro/RemnaManager/internal/system/stats"
	"github.com/ConsolePro/RemnaManager/internal/updater"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"path/filepath"
	"strings"
	"testing"
)

func testModel(t *testing.T, initialized bool) Model {
	return New(context.Background(), config.Store{Path: filepath.Join(t.TempDir(), "config.json")}, config.Config{Language: "ru", Email: "admin@example.com", Initialized: initialized}, updater.Checker{}, installer.Demo{})
}
func press(m *Model, key tea.KeyType) tea.Cmd {
	next, cmd := m.Update(tea.KeyMsg{Type: key})
	*m = next.(Model)
	return cmd
}
func TestOnboardingAndSettings(t *testing.T) {
	m := testModel(t, false)
	if m.screen != "language" {
		t.Fatal(m.screen)
	}
	press(&m, tea.KeyDown)
	press(&m, tea.KeyEnter)
	if m.screen != "email" || m.cfg.Language != "en" {
		t.Fatal(m.screen, m.cfg)
	}
	m.inputs[0].SetValue("invalid")
	press(&m, tea.KeyEnter)
	if m.problem == "" {
		t.Fatal("validation missing")
	}
	m.inputs[0].SetValue("a@example.com")
	cmd := press(&m, tea.KeyEnter)
	next, _ := m.Update(cmd())
	m = next.(Model)
	if m.screen != "menu" || !m.cfg.Initialized {
		t.Fatal(m.screen, m.cfg)
	}
	c, e := m.store.Load()
	if e != nil || c.Email != "a@example.com" || c.Language != "en" {
		t.Fatal(c, e)
	}
	reopened := New(context.Background(), m.store, c, updater.Checker{}, installer.Demo{})
	if reopened.screen != "menu" {
		t.Fatal("onboarding repeated")
	}
	m.startSettings(true)
	press(&m, tea.KeyLeft)
	press(&m, tea.KeyEnter)
	press(&m, tea.KeyEsc)
	press(&m, tea.KeyEsc)
	if m.screen != "menu" || m.cfg.Language != "en" {
		t.Fatal("cancel changed language")
	}
}
func TestAllMenuPaths(t *testing.T) {
	var visit func([]item)
	visit = func(items []item) {
		for i, it := range items {
			m := testModel(t, true)
			m.frames = []frame{{items: items, cursor: i}}
			cmd := press(&m, tea.KeyEnter)
			if len(it.children) > 0 {
				if len(m.frames) != 2 {
					t.Fatal(it.ru)
				}
				press(&m, tea.KeyEsc)
				if m.frames[0].cursor != i {
					t.Fatal("cursor reset")
				}
				visit(it.children)
			} else {
				if m.screen == "menu" && it.action != "check" {
					t.Fatal(it.ru)
				}
				if it.action == "pending" && !strings.Contains(m.View(), "В разработке") {
					t.Fatal(it.ru)
				}
				_ = cmd
			}
		}
	}
	visit(menu())
}
func TestStatsDoNotResetFormOrMenu(t *testing.T) {
	m := testModel(t, true)
	press(&m, tea.KeyDown)
	next, _ := m.Update(statsMsg(stats.Snapshot{CPUs: 4}))
	m = next.(Model)
	if m.frames[0].cursor != 1 {
		t.Fatal("menu cursor reset")
	}
	m.startSettings(true)
	press(&m, tea.KeyEnter)
	m.inputs[0].SetValue("новый@example.com")
	m.inputs[0].CursorStart()
	position := m.inputs[0].Position()
	next, _ = m.Update(statsMsg(stats.Snapshot{CPUs: 8}))
	m = next.(Model)
	if m.inputs[0].Value() != "новый@example.com" || m.inputs[0].Position() != position {
		t.Fatal("form reset")
	}
	press(&m, tea.KeyDelete)
	if m.inputs[0].Value() != "овый@example.com" {
		t.Fatal(m.inputs[0].Value())
	}
}
func TestUnicodePasteAndFieldNavigation(t *testing.T) {
	m := testModel(t, false)
	press(&m, tea.KeyEnter)
	m.inputs[0].SetValue("")
	pasted := "администратор+tag@example.com"
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(pasted), Paste: true})
	m = next.(Model)
	if m.inputs[0].Value() != pasted {
		t.Fatal(m.inputs[0].Value())
	}
	press(&m, tea.KeyLeft)
	press(&m, tea.KeyBackspace)
	if m.inputs[0].Value() == pasted {
		t.Fatal("backspace did not edit pasted text")
	}
	m.screen = "domains"
	m.inputs = append(m.inputs, newInput("sub.example.com", ""))
	press(&m, tea.KeyTab)
	if m.focus != 1 {
		t.Fatal(m.focus)
	}
	press(&m, tea.KeyShiftTab)
	if m.focus != 0 {
		t.Fatal(m.focus)
	}
}
func TestDemoAndStaleMessages(t *testing.T) {
	m := testModel(t, true)
	press(&m, tea.KeyEnter)
	cmd := press(&m, tea.KeyEnter)
	next, _ := m.Update(cmd())
	m = next.(Model)
	if m.screen != "environment" || m.environment == "" {
		t.Fatal(m.screen)
	}
	press(&m, tea.KeyEnter)
	m.inputs[0].SetValue("panel.example.com")
	m.inputs[1].SetValue("sub.example.com")
	press(&m, tea.KeyEnter)
	press(&m, tea.KeyEnter)
	if m.screen != "confirm" {
		t.Fatal(m.screen)
	}
	cmd = press(&m, tea.KeyEnter)
	finishInstallCommands(&m, cmd)
	if m.stage != 7 || !strings.Contains(m.View(), "100%") {
		t.Fatal(m.stage, m.logs)
	}
	oldRun := m.run
	press(&m, tea.KeyEsc)
	next, _ = m.Update(stepMsg{run: oldRun, step: 0})
	m = next.(Model)
	if m.screen != "menu" {
		t.Fatal("stale message reopened demo")
	}
}
func TestExitAndResize(t *testing.T) {
	m := testModel(t, true)
	for _, size := range [][2]int{{80, 30}, {36, 18}, {120, 50}, {20, 10}} {
		next, _ := m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		m = next.(Model)
		if m.View() == "" {
			t.Fatal(size)
		}
		if size[0] >= 36 && size[1] >= 18 {
			for _, line := range strings.Split(m.View(), "\n") {
				if lipgloss.Width(line) > size[0] {
					t.Fatalf("view exceeds terminal: %d > %d", lipgloss.Width(line), size[0])
				}
			}
			if lipgloss.Height(m.View()) > size[1] {
				t.Fatalf("view exceeds terminal height")
			}
		}
	}
	press(&m, tea.KeyF1)
	if !m.exitConfirm || m.exitYes {
		t.Fatal("exit not confirmed")
	}
	press(&m, tea.KeyEsc)
	if m.exitConfirm {
		t.Fatal("exit cancel failed")
	}
	press(&m, tea.KeyF1)
	press(&m, tea.KeyRight)
	cmd := press(&m, tea.KeyEnter)
	if cmd == nil {
		t.Fatal("no quit command")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatal("not quit")
	}
}
