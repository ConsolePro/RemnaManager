package ui

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/ConsolePro/RemnaManager/internal/installer"
	"github.com/ConsolePro/RemnaManager/internal/services"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

type routeServiceFake struct {
	installer.Demo
	request services.RoutingRequest
}

func (f *routeServiceFake) ManageRouting(_ context.Context, req services.RoutingRequest) (services.RoutingResult, error) {
	f.request = req
	return services.RoutingResult{Hosts: []services.RoutingHost{{UUID: "21111111-2222-4333-8444-555555555555", Remark: "FN1501", Address: "192.0.2.10"}}, Templates: []services.RoutingTemplate{{UUID: "11111111-2222-4333-8444-555555555555", Name: "Default", Type: "XRAY_JSON"}}}, nil
}

func routeEditorFixture(t *testing.T) (Model, *routeServiceFake) {
	t.Helper()
	m := testModel(t, true)
	f := &routeServiceFake{}
	m.service = f
	next, cmd := m.openRouting("route-create")
	m = next.(Model)
	next, _ = m.Update(cmd())
	m = next.(Model)
	press(&m, tea.KeyEnter)
	press(&m, tea.KeySpace)
	press(&m, tea.KeyEnter)
	if m.screen != "routeEditor" || len(m.routes.Spec.Pools[0].Hosts) != 1 {
		t.Fatal("cannot enter the editor", m.screen, m.routes.Spec)
	}
	return m, f
}

func TestRoutingWizardEditsGroupsRulesAndDefault(t *testing.T) {
	m, _ := routeEditorFixture(t)
	m.routes.Cursor = 0
	press(&m, tea.KeyEnter)
	if m.screen != "routeNameInput" || len(m.inputs) != 1 {
		t.Fatal("name form was lost by copying the model")
	}
	m.inputs[0].SetValue("RU_direct")
	press(&m, tea.KeyEnter)
	m.routes.Cursor = 1
	press(&m, tea.KeyEnter)
	m.routes.Cursor = 1
	press(&m, tea.KeyEnter)
	m.inputs[0].SetValue("backup")
	press(&m, tea.KeyEnter)
	press(&m, tea.KeySpace)
	press(&m, tea.KeyEnter)
	if len(m.routes.Spec.Pools) != 2 || m.routes.Spec.Pools[1].Name != "backup" {
		t.Fatal(m.routes.Spec)
	}
	m.routes.Cursor = 3
	press(&m, tea.KeyEnter)
	press(&m, tea.KeyEnter)
	m.inputs[0].SetValue("domain:example.com")
	for i := 0; i < 5; i++ {
		press(&m, tea.KeyEnter)
	}
	press(&m, tea.KeyEnter) // direct
	if m.screen != "routeRules" || m.routes.Spec.Rules[0].Domains[0] != "domain:example.com" {
		t.Fatal(m.screen, m.routes.Spec.Rules)
	}
	press(&m, tea.KeyEsc)
	m.routes.Cursor = 4
	press(&m, tea.KeyEnter)
	m.routes.Cursor = 3 // backup
	press(&m, tea.KeyEnter)
	if m.routes.Spec.DefaultTarget != "backup" {
		t.Fatal(m.routes.Spec.DefaultTarget)
	}
}

func TestRoutingCancelledResponseCannotChangeEditorAndRequestIsFrozen(t *testing.T) {
	m, f := routeEditorFixture(t)
	next, cmd := m.startRouting(services.RoutingRequest{Action: "route-preview", Spec: m.routes.Spec})
	m = next.(Model)
	m.routes.Spec.Pools[0].Hosts[0] = "edited-after-start"
	press(&m, tea.KeyEsc)
	next, _ = m.Update(cmd())
	m = next.(Model)
	if m.screen != "routeEditor" || f.request.Spec.Pools[0].Hosts[0] == "edited-after-start" {
		t.Fatal("cancelled response or shared request changed the editor")
	}
}

func TestRoutingResultDoesNotRetainPreviewAndDeleteBackReturnsToDetails(t *testing.T) {
	m := testModel(t, true)
	m.screen, m.run, m.routes.Text = "routeProgress", 7, "old preview"
	next, _ := m.Update(routingMsg{run: 7, err: errors.New("failed")})
	m = next.(Model)
	if m.routes.Text != "" || m.screen != "routeResult" || m.problem == "" {
		t.Fatal(m.screen, m.routes.Text, m.problem)
	}
	m.routes.Operation = "route-list"
	m.screen = "routeConfirm"
	press(&m, tea.KeyEsc)
	if m.screen != "routeDetails" {
		t.Fatal(m.screen)
	}
}

func TestRoutingFocusedFieldVisibleInSmallTerminal(t *testing.T) {
	m, _ := routeEditorFixture(t)
	m.width, m.height = 50, 18
	m.screen, m.routes.Cursor = "routeEditor", 3
	press(&m, tea.KeyEnter)
	press(&m, tea.KeyEnter)
	for i := 0; i < 4; i++ {
		press(&m, tea.KeyTab)
	}
	view := ansi.Strip(m.View())
	if !strings.Contains(view, "Порты") || strings.Count(view, "\n") > m.height {
		t.Fatal("focused field is hidden or view overflows", view)
	}
}
