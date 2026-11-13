package ui

import (
	"context"
	"errors"
	"testing"

	"github.com/ConsolePro/RemnaManager/internal/services"
	tea "github.com/charmbracelet/bubbletea"
)

type failedPreflight struct{ liveFake }

func (*failedPreflight) Preflight(context.Context, services.PanelParameters) (string, error) {
	return "", errors.New("SSH authentication failed")
}
func TestFailedPreflightReturnsToEditableWizard(t *testing.T) {
	m := testModel(t, true)
	m.service = &failedPreflight{}
	m.prepareInstallInputs()
	m.screen = "protectionOptions"
	cmd := press(&m, tea.KeyEnter)
	next, _ := m.Update(cmd())
	m = next.(Model)
	if m.screen != "protectionOptions" || m.problem == "" {
		t.Fatal("failed checks reached confirmation", m.screen)
	}
}
func TestCertificateManagementUsesExistingOperation(t *testing.T) {
	m := testModel(t, true)
	m.service = &managedFake{}
	next, _ := m.openPanelOperation(operation("Certificates", "Certificates", "panel-certificates"))
	m = next.(Model)
	if m.screen != "certificateOptions" {
		t.Fatal(m.screen)
	}
	press(&m, tea.KeyEnter)
	if m.screen != "panelConfirm" || m.panelRequest.Action != "panel-certificates" || m.panelRequest.Certificate.Method != "acme" {
		t.Fatal("certificate edit started a new installation", m.screen)
	}
}
