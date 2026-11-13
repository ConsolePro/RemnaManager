package ui

import (
	"testing"

	"github.com/ConsolePro/RemnaManager/internal/services"
	tea "github.com/charmbracelet/bubbletea"
)

func TestRemoteSubscriptionTrustPreservesInstallInputs(t *testing.T) {
	m := testModel(t, true)
	m.service = &managedFake{}
	m.prepareInstallInputs()
	m.inputs[0].SetValue("panel.example.test")
	m.inputs[1].SetValue("sub.example.test")
	m.installRemote = true
	m.remoteInstallWizard = true
	m.panelInstallInputs = m.inputs
	next, _ := m.openRemoteInputs()
	m = next.(Model)
	remote := services.SubscriptionRemote{SSH: services.NodeSSH{Host: "192.0.2.40", Port: 22, HostKey: "pinned-key"}, User: "root", Proxy: "caddy"}
	m.screen = "panelProgress"
	m.panelRequest = services.PanelRequest{Action: "sub-ssh-scan"}
	next, _ = m.receivePanelResult(panelResultMsg{run: m.run, result: services.PanelResult{Remote: remote, Fingerprint: "SHA256:verified"}})
	m = next.(Model)
	if m.screen != "panelRemoteTrust" {
		t.Fatal("fingerprint was not presented")
	}
	press(&m, tea.KeyEnter)
	p := m.parameters()
	if m.screen != "confirm" || p.PanelDomain != "panel.example.test" || p.SubscriptionDomain != "sub.example.test" || p.RemoteSubscription == nil || p.RemoteSubscription.SSH.HostKey != "pinned-key" {
		t.Fatal("remote trust lost panel inputs", m.screen, p)
	}
	press(&m, tea.KeyEsc)
	if m.screen != "domains" {
		t.Fatal("cannot return from remote confirmation")
	}
}

func TestAbsentPanelMenuKeepsBackupsAccessible(t *testing.T) {
	m := testModel(t, true)
	m.service = &managedFake{}
	m.screen = "panelProgress"
	m.panelRequest = services.PanelRequest{Action: "panel-inspect"}
	next, _ := m.receivePanelResult(panelResultMsg{run: m.run, result: services.PanelResult{Installation: "absent"}})
	m = next.(Model)
	items := m.frames[len(m.frames)-1].items
	if len(items) != 2 || items[0].action != "install" || items[1].en != "Backups and restore" {
		t.Fatal("backups disappeared after removal", items)
	}
}
