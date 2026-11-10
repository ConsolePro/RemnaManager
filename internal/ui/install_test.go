package ui

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/ConsolePro/RemnaManager/internal/installer"
	"github.com/ConsolePro/RemnaManager/internal/services"
	tea "github.com/charmbracelet/bubbletea"
)

type liveFake struct {
	steps, confirmed int
	proxy            string
}

func (*liveFake) Check(context.Context) (string, error) { return "Linux · SSH 2222", nil }
func (*liveFake) Stages() [][2]string                   { return installer.NewReal().Stages() }
func (s *liveFake) Step(ctx context.Context, _ int, p services.PanelParameters) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	s.steps++
	s.proxy = p.Proxy
	return nil
}
func (*liveFake) Defaults() services.PanelParameters {
	return services.PanelParameters{Proxy: "nginx", Admin: "admin", SSHPorts: []int{2222}}
}
func (*liveFake) Summary() string                 { return "Panel https://panel.example.com" }
func (s *liveFake) Confirm(context.Context) error { s.confirmed++; return nil }
func (*liveFake) Close() error                    { return nil }

func TestRealWizardRequiresExplicitFirewallConfirmation(t *testing.T) {
	m := testModel(t, true)
	service := &liveFake{}
	m.service = service
	press(&m, tea.KeyEnter)
	cmd := press(&m, tea.KeyEnter)
	next, _ := m.Update(cmd())
	m = next.(Model)
	press(&m, tea.KeyEnter)
	if m.screen != "installOptions" {
		t.Fatal(m.screen)
	}
	press(&m, tea.KeyRight)
	press(&m, tea.KeyEnter)
	if len(m.inputs) != 4 || m.inputs[3].Value() != "2222" {
		t.Fatal("effective SSH port was not prefilled")
	}
	m.inputs[0].SetValue("panel.example.com")
	m.inputs[1].SetValue("sub.example.com")
	for i := 0; i < 4; i++ {
		press(&m, tea.KeyEnter)
	}
	if m.screen != "confirm" || !strings.Contains(m.body(), "РЕАЛЬНАЯ УСТАНОВКА") || !strings.Contains(m.body(), "2222") {
		t.Fatal(m.screen, m.body())
	}
	cmd = press(&m, tea.KeyEnter)
	for cmd != nil {
		next, c := m.Update(cmd())
		m = next.(Model)
		cmd = c
	}
	if m.screen != "firewallVerify" || service.confirmed != 0 || service.steps != 9 || service.proxy != "caddy" {
		t.Fatal(m.screen, service)
	}
	cmd = press(&m, tea.KeyEnter)
	next, _ = m.Update(cmd())
	m = next.(Model)
	if m.screen != "installResult" || service.confirmed != 1 {
		t.Fatal("firewall was not explicitly committed")
	}
}
func TestCancelStopsInstallationContext(t *testing.T) {
	m := testModel(t, true)
	m.service = &liveFake{}
	m.screen = "progress"
	m.installCtx, m.installCancel = context.WithCancel(m.ctx)
	ctx := m.installCtx
	run := m.run
	press(&m, tea.KeyEsc)
	if !errors.Is(ctx.Err(), context.Canceled) {
		t.Fatal("installation was left running")
	}
	next, cmd := m.Update(stepMsg{run: run, step: 0})
	m = next.(Model)
	if cmd != nil || m.screen != "menu" {
		t.Fatal("cancelled step restarted execution")
	}
}
