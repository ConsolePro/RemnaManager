package ui

import (
	"context"
	"errors"
	"os"
	"path/filepath"
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
	case "node-install-connect-scan", "node-install-scan", "node-install-rescan":
		result.SSH = request.Install.SSH
		result.Fingerprint = "SHA256:verified-test-key"
	case "node-install-create":
		f.status = services.NodeInstallStatus{ID: strings.Repeat("a", 32), Params: request.Install, Completed: make([]bool, 9)}
	case "node-install-connect-create":
		f.status = services.NodeInstallStatus{Version: 4, DetailsPending: true, ID: strings.Repeat("a", 32), Params: request.Install, Completed: make([]bool, 9)}
	case "node-install-details":
		identity := f.status.Params.SSH
		f.status.Params = request.Install
		f.status.Params.SSH = identity
		f.status.DetailsPending = false
	case "node-install-pending":
		result.Jobs = []services.NodeInstallStatus{f.status}
	case "node-install-detect-panel-ip":
		result.Text = "192.0.2.20"
	case "node-install-panel-ip":
		f.status.Params.PanelIP = request.Install.PanelIP
	case "node-install-key":
		f.status.Params.SSH.IdentityFile = filepath.Join(os.TempDir(), "managed-node-key")
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
	if request.Action != "node-install-connect-scan" && request.Action != "node-install-scan" && request.Action != "node-install-rescan" && request.Action != "node-install-pending" {
		status := f.status
		result.Install = &status
	}
	return result, nil
}

func TestNodeInstallDetectsPanelIPBeforeRemoteStages(t *testing.T) {
	m := testModel(t, true)
	service := &provisionFake{status: services.NodeInstallStatus{ID: strings.Repeat("c", 32), Completed: make([]bool, 9)}}
	m.service = service
	m.nodeAction = "node-install-add"
	m.nodeInstallJob = &service.status
	m.screen = "nodesProgress"
	next, cmd := m.Update(nodeResultMsg{run: m.run, action: "node-install-authenticated", result: services.NodeResult{Install: &service.status}})
	m = next.(Model)
	next, cmd = m.Update(cmd())
	m = next.(Model)
	// Bootstrap the managed key before detecting addresses or changing services.
	if len(service.requests) != 1 || service.requests[0].Action != "node-install-key" {
		t.Fatal("managed key bootstrap not requested")
	}
	// The key result schedules panel IP detection.
	next, _ = m.Update(cmd())
	m = next.(Model)
	if m.screen != "nodeInstallPanelIP" || m.inputs[0].Value() != "192.0.2.20" || len(service.requests) != 2 || service.requests[1].Action != "node-install-detect-panel-ip" {
		t.Fatal("detection did not pause for review", m.screen)
	}
	m.inputs[0].SetValue("127.0.0.1")
	if cmd := press(&m, tea.KeyEnter); cmd != nil || m.problem == "" {
		t.Fatal("invalid source address accepted")
	}
	m.inputs[0].SetValue("192.0.2.21")
	cmd = press(&m, tea.KeyEnter)
	next, cmd = m.Update(cmd())
	m = next.(Model)
	if service.status.Params.PanelIP != "192.0.2.21" || service.requests[2].Action != "node-install-panel-ip" || cmd == nil {
		t.Fatal("corrected address was not saved before stages")
	}
	next, _ = m.Update(cmd())
	m = next.(Model)
	if service.requests[3].Action != "node-install-step" || service.requests[3].Stage != 0 {
		t.Fatal("did not start preflight after address confirmation")
	}
}

func TestNodeInstallDetectionFailureAllowsManualAddress(t *testing.T) {
	m := testModel(t, true)
	m.nodeAction = "node-install-add"
	m.nodeInstallJob = &services.NodeInstallStatus{ID: strings.Repeat("d", 32)}
	m.screen = "nodesProgress"
	next, _ := m.Update(nodeResultMsg{run: m.run, action: "node-install-detect-panel-ip", err: errors.New("SSH address unavailable")})
	m = next.(Model)
	if m.screen != "nodeInstallPanelIP" || m.inputs[0].Value() != "" || m.problem == "" {
		t.Fatal("no manual fallback after detection failure")
	}
	press(&m, tea.KeyEsc)
	if m.screen != "nodeInstallResult" || m.nodeInstallJob == nil {
		t.Fatal("cancelling address confirmation lost the resumable journal")
	}
}

func TestNodeInstallWizardSavesBeforeAuthAndRequiresSSHCommit(t *testing.T) {
	m := testModel(t, true)
	service := &provisionFake{}
	m.service = service
	next, _ := m.openNodeOperation(operation("???????? ????", "Add node", "node-install-add"))
	m = next.(Model)
	if m.screen != "nodeConnectInput" || len(m.inputs) != 3 {
		t.Fatal("connection must come first", m.screen)
	}
	values := []string{"node.example.test", "22", ""}
	for i, value := range values {
		m.inputs[i].SetValue(value)
	}
	var cmd tea.Cmd
	for range values {
		cmd = press(&m, tea.KeyEnter)
	}
	next, cmd = m.Update(cmd())
	m = next.(Model)
	if m.screen != "nodesProgress" || service.requests[0].Action != "node-install-connect-scan" {
		t.Fatal("unexpected first connection", m.screen)
	}
	next, cmd = m.Update(cmd())
	m = next.(Model)
	if service.requests[1].Action != "node-install-connect-create" || m.nodeInstallJob == nil {
		t.Fatal("connection was not saved before login")
	}
	next, _ = m.Update(cmd())
	m = next.(Model)
	if service.requests[2].Action != "node-install-auth" {
		t.Fatal("root authentication not requested")
	}
	next, cmd = m.Update(nodeResultMsg{run: m.run, action: "node-install-authenticated", result: services.NodeResult{Install: &service.status}})
	m = next.(Model)
	next, _ = m.Update(cmd())
	m = next.(Model)
	if m.screen != "nodeDetailsInput" || service.requests[3].Action != "node-install-key" || service.status.Params.SSH.IdentityFile == "" {
		t.Fatal("details shown before key verification", m.screen)
	}
	details := []string{"New_Node", "DE", "New_Node_Profile", "3333", "cover.example.test", "studio", "192.0.2.20", "Finland 🇫🇮"}
	for i, value := range details {
		m.inputs[i].SetValue(value)
	}
	for range len(details) - 2 {
		press(&m, tea.KeyTab)
	}
	if cmd = press(&m, tea.KeyEnter); cmd != nil || m.screen != "nodeInstallConfirm" {
		t.Fatal("missing final installation consent", m.screen, m.problem)
	}
	cmd = press(&m, tea.KeyEnter)
	for cmd != nil {
		next, cmd = m.Update(cmd())
		m = next.(Model)
	}
	if service.status.Params.CountryCode != "DE" || service.status.Params.APIPort != 3333 || service.status.Params.ProfileName != "New_Node" || service.status.Params.HostName != details[7] {
		t.Fatal("details lost")
	}
	if m.screen != "nodeInstallResult" || !m.nodeInstallJob.Ready || service.requests[len(service.requests)-1].Action != "node-install-confirm" {
		t.Fatal("firewall confirmation lost")
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
	if m.screen != "nodesProgress" || m.nodeInstallJob.ID != service.status.ID {
		t.Fatal("pending journal was not loaded")
	}
	for _, request := range service.requests {
		if request.Action == "node-install-create" {
			t.Fatal("resume created a new journal")
		}
	}
	press(&m, tea.KeyEsc)
	if m.screen != "menu" {
		t.Fatal("resume back navigation did not leave safely")
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
