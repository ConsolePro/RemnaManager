package ui

import (
	"context"
	"strings"
	"testing"

	"github.com/ConsolePro/RemnaManager/internal/services"
	tea "github.com/charmbracelet/bubbletea"
)

type provisionFake struct {
	liveFake
	requests []services.NodeRequest
	status   services.NodeInstallStatus
}

func (f *provisionFake) ManageNodes(ctx context.Context, request services.NodeRequest) (services.NodeResult, error) {
	if ctx.Err() != nil {
		return services.NodeResult{}, ctx.Err()
	}
	f.requests = append(f.requests, request)
	result := services.NodeResult{}
	switch request.Action {
	case "node-install-scan", "node-install-rescan":
		result.SSH = request.Install.SSH
		result.Fingerprint = "SHA256:verified-test-key"
	case "node-install-create":
		f.status = services.NodeInstallStatus{ID: strings.Repeat("a", 32), Params: request.Install, Completed: make([]bool, 9)}
	case "node-install-pending":
		result.Jobs = []services.NodeInstallStatus{f.status}
	case "node-install-step":
		f.status.Completed[request.Stage] = true
		f.status.Stage = request.Stage + 1
		if request.Stage == 7 {
			f.status.FirewallPending = true
		}
	case "node-install-confirm":
		f.status.Completed[8] = true
		f.status.Ready = true
		f.status.FirewallPending = false
	}
	if request.Action != "node-install-scan" && request.Action != "node-install-rescan" && request.Action != "node-install-pending" {
		status := f.status
		result.Install = &status
	}
	return result, nil
}

func TestNodeInstallWizardSavesBeforeAuthAndRequiresSSHCommit(t *testing.T) {
	m := testModel(t, true)
	service := &provisionFake{}
	m.service = service
	next, _ := m.openNodeOperation(operation("Добавить ноду", "Add node", "node-install-add"))
	m = next.(Model)
	values := []string{"New_Node", "node.example.test", "22", "cover.example.test", "192.0.2.20", ""}
	for i, value := range values {
		m.inputs[i].SetValue(value)
	}
	var cmd tea.Cmd
	for range values {
		cmd = press(&m, tea.KeyEnter)
	}
	next, _ = m.Update(cmd())
	m = next.(Model)
	if m.screen != "nodesTrust" || len(service.requests) != 1 {
		t.Fatal(m.screen)
	}
	m.inputs[0].SetValue("SHA256:verified-test-key")
	press(&m, tea.KeyEnter)
	if m.screen != "nodeInstallConfirm" || len(service.requests) != 1 {
		t.Fatal("installation started without final confirmation")
	}
	cmd = press(&m, tea.KeyEnter)
	next, cmd = m.Update(cmd())
	m = next.(Model)
	if service.requests[1].Action != "node-install-create" || m.nodeInstallJob == nil {
		t.Fatal("journal was not created before SSH authentication")
	}
	next, _ = m.Update(cmd())
	m = next.(Model)
	if service.requests[2].Action != "node-install-auth" {
		t.Fatal("SSH authentication was not requested")
	}
	// The real TUI suspends itself for native OpenSSH; emulate its completion.
	next, cmd = m.Update(nodeResultMsg{run: m.run, action: "node-install-authenticated", result: services.NodeResult{Install: &service.status}})
	m = next.(Model)
	for cmd != nil {
		next, c := m.Update(cmd())
		m = next.(Model)
		cmd = c
	}
	if m.screen != "nodeInstallVerify" || service.status.Ready || !service.status.FirewallPending {
		t.Fatal("firewall was committed without explicit SSH verification")
	}
	cmd = press(&m, tea.KeyEnter)
	next, _ = m.Update(cmd())
	m = next.(Model)
	if m.screen != "nodeInstallResult" || !m.nodeInstallJob.Ready || service.requests[len(service.requests)-1].Action != "node-install-confirm" {
		t.Fatal("firewall confirmation was lost")
	}
}

func TestPendingNodeInstallLoadsAndRescansInsteadOfCreating(t *testing.T) {
	m := testModel(t, true)
	service := &provisionFake{status: services.NodeInstallStatus{ID: strings.Repeat("b", 32), Params: services.NodeInstallParameters{Name: "Saved_Node", Address: "node.example.test"}, Completed: make([]bool, 9)}}
	m.service = service
	next, cmd := m.openNodeOperation(operation("Незавершённые установки", "Pending installations", "node-install-pending"))
	m = next.(Model)
	next, _ = m.Update(cmd())
	m = next.(Model)
	if m.screen != "nodeInstallPending" {
		t.Fatal(m.screen)
	}
	cmd = press(&m, tea.KeyEnter)
	next, cmd = m.Update(cmd())
	m = next.(Model)
	next, _ = m.Update(cmd())
	m = next.(Model)
	if m.screen != "nodesTrust" || m.nodeInstallJob.ID != service.status.ID {
		t.Fatal("pending journal was not loaded")
	}
	for _, request := range service.requests {
		if request.Action == "node-install-create" {
			t.Fatal("resume created a new journal")
		}
	}
	press(&m, tea.KeyEsc)
	if m.screen != "nodeInstallPending" {
		t.Fatal("resume back navigation lost the pending list")
	}
}

func TestLeavingNodeFirewallVerificationKeepsAutomaticRollback(t *testing.T) {
	m := testModel(t, true)
	service := &provisionFake{}
	m.service = service
	m.screen = "nodeInstallVerify"
	m.nodeInstallJob = &services.NodeInstallStatus{ID: strings.Repeat("c", 32), FirewallPending: true}
	press(&m, tea.KeyEsc)
	if m.screen != "nodeInstallResult" || !m.nodeInstallJob.FirewallPending || len(service.requests) != 0 {
		t.Fatal("leaving verification committed or disarmed the remote rollback")
	}
}
