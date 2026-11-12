package ui

import (
	"strings"
	"testing"

	"github.com/ConsolePro/RemnaManager/internal/services"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
)

func TestNodeCountryPickerRestoresTypedDetails(t *testing.T) {
	m := testModel(t, true)
	m.nodeInstallJob = &services.NodeInstallStatus{Version: 2, ID: strings.Repeat("a", 32), DetailsPending: true}
	m.nodeInstallParams = services.NodeInstallParameters{Address: "node.test", SSH: services.NodeSSH{Port: 22}}
	m.prepareNodeDetails()
	m.screen = "nodeDetailsInput"
	m.inputs[0].SetValue("My_Node")
	m.focus = 1
	press(&m, tea.KeyEnter)
	if m.screen != "nodeCountry" {
		t.Fatal(m.screen)
	}
	m.inputs[0].SetValue("Германия")
	press(&m, tea.KeyEnter)
	if m.screen != "nodeDetailsInput" || m.inputs[0].Value() != "My_Node" || m.inputs[1].Value() != "DE" || m.focus != 2 {
		t.Fatal("country picker lost details")
	}
}

func TestNodeWizardUsesProfileNameForEmptyHost(t *testing.T) {
	m := testModel(t, true)
	m.nodeConnectWizard = true
	m.nodeInstallJob = &services.NodeInstallStatus{Version: 3, ID: strings.Repeat("a", 32)}
	m.nodeInstallParams = services.NodeInstallParameters{Name: "Finland", ProfileName: "FN1001", Address: "192.0.2.10", Domain: "cover.example.test", SSH: services.NodeSSH{Host: "192.0.2.10", Port: 22}}
	m.prepareNodeDetails()
	m.screen = "nodeDetailsInput"
	m.focus = 7
	press(&m, tea.KeyEnter)
	if m.screen != "nodeInstallConfirm" || m.nodeInstallParams.HostName != "" {
		t.Fatal("empty host should use the profile name", m.screen, m.problem)
	}
	view := m.nodeInstallView()
	if strings.Count(view, "VLESS_REALITY_FN1001") != 2 {
		t.Fatal("confirmation should show matching profile and default host", view)
	}
}

func TestFirstTrustCannotAcceptChangedKeyAndHelpFitsNormalTerminal(t *testing.T) {
	m := testModel(t, true)
	m.nodeConnectWizard = true
	m.screen = "nodesTrust"
	m.nodeTitle = "Добавить ноду"
	m.nodeAction = "node-install-add"
	m.nodeSSH.Host = "node.test"
	m.nodeFingerprint = "SHA256:verified-test-key"
	m.inputs = []textinput.Model{newInput("SHA256:…", "")}
	m.width, m.height = 80, 30
	if view := m.View(); !strings.Contains(view, "Tab") || !strings.Contains(view, "ssh-keygen") {
		t.Fatal("first-use explanation hidden", view)
	}
	m.nodeSSHChanged = true
	press(&m, tea.KeyTab)
	if m.nodeTrustFirst {
		t.Fatal("changed key accepted through first-use shortcut")
	}
	press(&m, tea.KeyEnter)
	if m.problem == "" || m.screen != "nodesTrust" {
		t.Fatal("changed key bypassed verification")
	}
}
