package ui

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/ConsolePro/RemnaManager/internal/services"
	tea "github.com/charmbracelet/bubbletea"
)

type nodeFake struct {
	liveFake
	requests []services.NodeRequest
}

func (f *nodeFake) ManageNodes(ctx context.Context, request services.NodeRequest) (services.NodeResult, error) {
	if ctx.Err() != nil {
		return services.NodeResult{}, ctx.Err()
	}
	f.requests = append(f.requests, request)
	if request.Action == "nodes-list" {
		return services.NodeResult{Nodes: []services.Node{{UUID: "11111111-2222-4333-8444-555555555555", Name: "Тестовая нода", Address: "192.0.2.10", Port: 2222}}}, nil
	}
	return services.NodeResult{TextRU: "Операция выполнена", Text: "Operation complete"}, nil
}
func TestNodeRenameRequiresSelectionAndConfirmation(t *testing.T) {
	m := testModel(t, true)
	service := &nodeFake{}
	m.service = service
	next, cmd := m.openNodeOperation(operation("Переименовать", "Rename", "node-rename"))
	m = next.(Model)
	next, _ = m.Update(cmd())
	m = next.(Model)
	if m.screen != "nodesList" || len(service.requests) != 1 {
		t.Fatal(m.screen)
	}
	press(&m, tea.KeyEnter)
	if m.screen != "nodesInput" {
		t.Fatal(m.screen)
	}
	m.inputs[0].SetValue("Новое имя")
	press(&m, tea.KeyEnter)
	if m.screen != "nodesConfirm" || len(service.requests) != 1 || !strings.Contains(m.View(), "Новое имя") {
		t.Fatal("rename happened without confirmation")
	}
	cmd = press(&m, tea.KeyEnter)
	next, _ = m.Update(cmd())
	m = next.(Model)
	if m.screen != "nodesResult" || len(service.requests) != 2 || service.requests[1].Value != "Новое имя" {
		t.Fatal("rename request was lost")
	}
}
func TestCancelledNodeRequestDoesNotReopenScreen(t *testing.T) {
	m := testModel(t, true)
	m.service = &nodeFake{}
	next, cmd := m.openNodeOperation(operation("Список нод", "Node list", "nodes-list"))
	m = next.(Model)
	ctx := m.installCtx
	press(&m, tea.KeyEsc)
	if !errors.Is(ctx.Err(), context.Canceled) {
		t.Fatal("node request still running")
	}
	next, _ = m.Update(cmd())
	m = next.(Model)
	if m.screen != "menu" {
		t.Fatal("late result reopened the node list")
	}
}
func TestNodeSSHTrustRequiresTypedFingerprint(t *testing.T) {
	m := testModel(t, true)
	m.service = &nodeFake{}
	m.screen = "nodesTrust"
	m.nodeAction = "node-ssh"
	m.nodeFingerprint = "SHA256:verified"
	m.inputs = append(m.inputs, newInput("", "wrong"))
	press(&m, tea.KeyEnter)
	if m.screen != "nodesTrust" || m.problem == "" {
		t.Fatal("wrong fingerprint accepted")
	}
	m.inputs[0].SetValue("SHA256:verified")
	press(&m, tea.KeyEnter)
	if m.screen != "nodesConfirm" || m.nodeRequest.Fingerprint != m.nodeFingerprint {
		t.Fatal("fingerprint was not passed to final verification")
	}
}
