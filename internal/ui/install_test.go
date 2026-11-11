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
	installed        bool
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
func (s *liveFake) Installed() bool               { return s.installed }
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
	press(&m, tea.KeyEnter) // Default Cookie protection.
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

func TestInstalledPanelShowsCredentialsWithoutRunningWizard(t *testing.T) {
	m := testModel(t, true)
	service := &liveFake{installed: true}
	m.service = service
	press(&m, tea.KeyEnter)
	cmd := press(&m, tea.KeyEnter)
	next, cmd := m.Update(cmd())
	m = next.(Model)
	if m.screen != "installResult" || cmd != nil || service.steps != 0 {
		t.Fatal("completed installation was started again", m.screen, service.steps)
	}
	if !strings.Contains(m.body(), "panel.example.com") || !strings.Contains(m.body(), "Ctrl+Shift+C") {
		t.Fatal("credentials and copy instructions are missing", m.body())
	}
}

func TestProxyChoicesVisibleAndSelectableWithTab(t *testing.T) {
	m := testModel(t, true)
	m.service = &liveFake{}
	m.prepareInstallInputs()
	m.screen = "installOptions"
	for _, name := range []string{"Nginx", "Caddy"} {
		if !strings.Contains(m.body(), name) {
			t.Fatal("hidden proxy choice", name)
		}
	}
	press(&m, tea.KeyTab)
	press(&m, tea.KeyEnter)
	press(&m, tea.KeyEnter)
	if m.parameters().Proxy != "caddy" {
		t.Fatal("Caddy choice was not passed to the installer")
	}
}

func TestCtrlCRequiresConfirmationAndKeepsOperationAlive(t *testing.T) {
	for _, screen := range []string{"progress", "firewallVerify", "installResult", "panelResult"} {
		t.Run(screen, func(t *testing.T) {
			m := testModel(t, true)
			m.screen = screen
			m.installCtx, m.installCancel = context.WithCancel(m.ctx)
			ctx := m.installCtx
			defer m.cancelInstall()
			if cmd := press(&m, tea.KeyCtrlC); cmd != nil || !m.exitConfirm || m.exitYes || ctx.Err() != nil {
				t.Fatal("Ctrl+C closed the screen or cancelled the operation")
			}
			press(&m, tea.KeyEnter) // Default: stay in the application.
			if m.exitConfirm || m.screen != screen || ctx.Err() != nil {
				t.Fatal("default exit choice lost the screen")
			}
			press(&m, tea.KeyCtrlC)
			press(&m, tea.KeyRight)
			cmd := press(&m, tea.KeyEnter)
			if cmd == nil || ctx.Err() == nil {
				t.Fatal("confirmed exit did not cancel the operation")
			}
			if _, ok := cmd().(tea.QuitMsg); !ok {
				t.Fatal("confirmed exit did not quit")
			}
		})
	}
}

func TestTinyAuthWizardAndSwitchBackToCookie(t *testing.T) {
	m := testModel(t, true)
	m.service = &liveFake{}
	m.prepareInstallInputs()
	m.screen = "installOptions"
	press(&m, tea.KeyEnter)
	if m.screen != "protectionOptions" || m.protectionChoice != 0 {
		t.Fatal("Cookie must be the default protection")
	}
	press(&m, tea.KeyDown)
	press(&m, tea.KeyEnter)
	if m.screen != "domains" || len(m.inputs) != 5 {
		t.Fatal("TinyAuth domain field missing")
	}
	for i, value := range []string{"panel.example.com", "sub.example.com", "admin", "2222", "auth.panel.example.com"} {
		m.inputs[i].SetValue(value)
	}
	for range m.inputs {
		press(&m, tea.KeyEnter)
	}
	if m.screen != "confirm" || m.parameters().Protection != "tinyauth" || m.parameters().AuthDomain != "auth.panel.example.com" {
		t.Fatal("TinyAuth parameters were lost")
	}
	press(&m, tea.KeyEsc)
	press(&m, tea.KeyEsc)
	press(&m, tea.KeyUp)
	press(&m, tea.KeyEnter)
	if m.screen != "domains" || len(m.inputs) != 4 || m.focus != 0 || m.parameters().Protection != "cookie" || m.parameters().AuthDomain != "" {
		t.Fatal("switching protection left stale TinyAuth fields")
	}
}

func TestAllThreeProtectionChoicesWithBothProxies(t *testing.T) {
	for proxy := 0; proxy < 2; proxy++ {
		for choice, mode := range protectionChoices {
			t.Run([]string{"nginx", "caddy"}[proxy]+"/"+mode, func(t *testing.T) {
				m := testModel(t, true)
				m.service = &liveFake{}
				m.prepareInstallInputs()
				m.proxyChoice = proxy
				m.screen = "protectionOptions"
				for i := 0; i < choice; i++ {
					press(&m, tea.KeyDown)
				}
				press(&m, tea.KeyEnter)
				p := m.parameters()
				if p.Protection != mode {
					t.Fatal("incorrect protection mode")
				}
				if choice == 1 && len(m.inputs) != 5 {
					t.Fatal("TinyAuth needs a domain")
				}
				if choice != 1 && (len(m.inputs) != 4 || p.AuthDomain != "") {
					t.Fatal("Cookie and MFA must not require an extra domain")
				}
			})
		}
	}
}
