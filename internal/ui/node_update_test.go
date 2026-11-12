package ui

import (
	"context"
	"strings"
	"testing"

	"github.com/ConsolePro/RemnaManager/internal/services"
	tea "github.com/charmbracelet/bubbletea"
)

type updateNodeFake struct{ nodeFake }

func (f *updateNodeFake) ManageNodes(ctx context.Context, req services.NodeRequest) (services.NodeResult, error) {
	if req.Action == "nodes-list" {
		return f.nodeFake.ManageNodes(ctx, req)
	}
	f.requests = append(f.requests, req)
	switch req.Action {
	case "node-core-prepare":
		return services.NodeResult{Version: "v26.9.9", Text: "Xray → v26.9.9"}, nil
	case "node-update-prepare", "node-reinstall-prepare":
		return services.NodeResult{SSH: services.NodeSSH{Host: "node.example.test", Port: 22}, Fingerprint: "SHA256:verified-update-key", Text: "remnawave/node@sha256:" + strings.Repeat("a", 64)}, nil
	case "node-core-auth", "node-update-auth", "node-reinstall-auth":
		return services.NodeResult{Command: []string{"ssh", "fixture"}}, nil
	}
	return services.NodeResult{TextRU: "Обновление завершено"}, nil
}

func TestNodeUpdateRequiresSelectionTrustAndExplicitConfirmation(t *testing.T) {
	checkNodeReplacementConfirmation(t, "node-update")
}

func TestNodeReinstallRequiresSelectionTrustAndExplicitConfirmation(t *testing.T) {
	checkNodeReplacementConfirmation(t, "node-reinstall")
}

func checkNodeReplacementConfirmation(t *testing.T, action string) {
	t.Helper()
	m := testModel(t, true)
	f := &updateNodeFake{}
	m.service = f
	next, cmd := m.openNodeOperation(operation("Операция с нодой", "Node operation", action))
	m = next.(Model)
	next, _ = m.Update(cmd())
	m = next.(Model)
	cmd = press(&m, tea.KeyEnter)
	next, _ = m.Update(cmd())
	m = next.(Model)
	if m.screen != "nodesConfirm" || len(f.requests) != 2 {
		t.Fatal("update must use saved SSH without manual fingerprint input", m.screen)
	}
	if m.screen != "nodesConfirm" || len(f.requests) != 2 || !strings.Contains(m.View(), "remnawave/node") {
		t.Fatal("update ran before confirmation")
	}
	cmd = press(&m, tea.KeyEnter)
	next, auth := m.Update(cmd())
	m = next.(Model)
	if auth == nil || f.requests[2].Action != action+"-auth" {
		t.Fatal("native authentication was not requested")
	}
	next, cmd = m.Update(nodeResultMsg{run: m.run, action: action + "-authenticated"})
	m = next.(Model)
	if len(f.requests) != 3 || cmd == nil {
		t.Fatal("update ran outside the managed command")
	}
	next, _ = m.Update(cmd())
	m = next.(Model)
	if m.screen != "nodesResult" || f.requests[3].Action != action+"-run" {
		t.Fatal("authenticated update did not finish")
	}
}

func TestNodeUpdateBackDoesNotOpenSSHSettings(t *testing.T) {
	m := testModel(t, true)
	m.nodeAction = "node-update"
	m.screen = "nodesTrust"
	press(&m, tea.KeyEsc)
	if m.screen != "nodesList" {
		t.Fatal("unexpected return from update trust", m.screen)
	}
	m.screen = "nodesConfirm"
	press(&m, tea.KeyEsc)
	if m.screen != "nodesList" {
		t.Fatal("back should return to node selection")
	}
}

func TestWebsiteChangeKeepsSelectionThroughAuthentication(t *testing.T) {
	m := testModel(t, true)
	f := &updateNodeFake{}
	m.service = f
	next, cmd := m.openNodeOperation(operation("Сайт", "Website", "node-site"))
	m = next.(Model)
	next, _ = m.Update(cmd())
	m = next.(Model)
	press(&m, tea.KeyEnter)
	if m.screen != "nodesInput" {
		t.Fatal(m.screen)
	}
	m.inputs[0].SetValue("atlas")
	press(&m, tea.KeyEnter)
	if m.screen != "nodesConfirm" || len(f.requests) != 1 {
		t.Fatal("website changed before confirmation")
	}
	cmd = press(&m, tea.KeyEnter)
	for cmd != nil {
		next, cmd = m.Update(cmd())
		m = next.(Model)
	}
	if m.screen != "nodesResult" || len(f.requests) != 3 || f.requests[2].Action != "node-site-run" || f.requests[2].Value != "atlas" {
		t.Fatal("selected website lost during authentication", f.requests)
	}
}

func TestXrayVersionSurvivesAuthenticationAndRequiresConfirmation(t *testing.T) {
	m := testModel(t, true)
	f := &updateNodeFake{}
	m.service = f
	next, cmd := m.openNodeOperation(operation("Ядро Xray", "Xray core", "node-core"))
	m = next.(Model)
	next, _ = m.Update(cmd())
	m = next.(Model)
	cmd = press(&m, tea.KeyEnter)
	next, _ = m.Update(cmd())
	m = next.(Model)
	if m.screen != "nodesConfirm" || m.nodeRequest.Value != "v26.9.9" || len(f.requests) != 2 {
		t.Fatal("core update ran before confirmation")
	}
	cmd = press(&m, tea.KeyEnter)
	next, _ = m.Update(cmd())
	m = next.(Model)
	next, cmd = m.Update(nodeResultMsg{run: m.run, action: "node-core-authenticated"})
	m = next.(Model)
	next, _ = m.Update(cmd())
	m = next.(Model)
	last := f.requests[len(f.requests)-1]
	if m.screen != "nodesResult" || last.Action != "node-core-run" || last.Value != "v26.9.9" {
		t.Fatal("selected Xray version lost during authentication")
	}
}
