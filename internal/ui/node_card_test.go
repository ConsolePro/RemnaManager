package ui

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/ConsolePro/RemnaManager/internal/services"
	tea "github.com/charmbracelet/bubbletea"
)

type cardNodeFake struct {
	nodeFake
	current bool
	deleted bool
}

func (f *cardNodeFake) ManageNodes(ctx context.Context, req services.NodeRequest) (services.NodeResult, error) {
	if req.Action == "nodes-list" {
		return f.nodeFake.ManageNodes(ctx, req)
	}
	f.requests = append(f.requests, req)
	switch req.Action {
	case "node-details":
		var node services.Node
		_ = json.Unmarshal([]byte(`{"uuid":"11111111-2222-4333-8444-555555555555","name":"FN1501","address":"192.0.2.10","isConnected":true,"versions":{"xray":"26.11.9","node":"2.7.0"},"xrayUptime":3660,"system":{"stats":{"uptime":90000}}}`), &node)
		return services.NodeResult{Nodes: []services.Node{node}}, nil
	case "node-core-check":
		if f.current {
			return services.NodeResult{TextRU: "Версия ядра Xray актуальная.", Versions: []string{"v26.10.31", "v26.9.9"}}, nil
		}
		return services.NodeResult{TextRU: "Доступна новая версия Xray: v26.11.9", Version: "v26.11.9"}, nil
	case "node-delete":
		f.deleted = true
	}
	return services.NodeResult{Text: "Done"}, nil
}

func openCard(t *testing.T, f *cardNodeFake) Model {
	t.Helper()
	m := testModel(t, true)
	m.service = f
	m.frames[0].cursor = 1 // Nodes opens its list directly from the root menu.
	cmd := press(&m, tea.KeyEnter)
	if cmd == nil {
		t.Fatal("nodes did not load directly")
	}
	next, _ := m.Update(cmd())
	m = next.(Model)
	if m.screen != "nodesList" {
		t.Fatal(m.screen)
	}
	cmd = press(&m, tea.KeyEnter)
	next, _ = m.Update(cmd())
	m = next.(Model)
	if m.screen != "nodesCard" {
		t.Fatal(m.screen)
	}
	return m
}

func TestNodeCardShowsMetricsAndActionsWithoutStatusSubmenu(t *testing.T) {
	m := openCard(t, &cardNodeFake{})
	m.width, m.height = 124, 40
	view := m.View()
	for _, text := range []string{"FN1501", "192.0.2.10", "26.11.9", "2.7.0", "1 ч 1 мин", "1 д 1 ч", "Ядро Xray", "Удалить"} {
		if !strings.Contains(view, text) {
			t.Fatal("missing card detail", text)
		}
	}
	if strings.Contains(view, "Статус ноды") || strings.Contains(view, "Откатить ядро") {
		t.Fatal("redundant node operations")
	}
	press(&m, tea.KeyEsc)
	if m.screen != "nodesList" {
		t.Fatal("card must return to node list")
	}
	press(&m, tea.KeyEsc)
	if m.screen != "menu" {
		t.Fatal("list must return to root menu")
	}
}

func TestUnifiedCoreOffersUpdateOrOlderReleases(t *testing.T) {
	for _, current := range []bool{false, true} {
		f := &cardNodeFake{current: current}
		m := openCard(t, f)
		m.nodeActionCursor = 1
		cmd := press(&m, tea.KeyEnter)
		next, _ := m.Update(cmd())
		m = next.(Model)
		if current {
			if m.screen != "nodesCoreVersions" {
				t.Fatal("up-to-date core must offer rollback", m.screen)
			}
			press(&m, tea.KeyEnter)
			if m.nodeRequest.Value != "v26.10.31" || m.nodeRequest.Action != "node-core-rollback-auth" {
				t.Fatal(m.nodeRequest)
			}
		} else if m.screen != "nodesConfirm" || m.nodeRequest.Value != "v26.11.9" {
			t.Fatal("new core must require update confirmation", m.screen)
		}
		press(&m, tea.KeyEsc)
		if m.screen != "nodesCard" {
			t.Fatal("core must return to selected node")
		}
	}
}

func TestDeleteNodeRequiresConfirmationAndReturnsToList(t *testing.T) {
	f := &cardNodeFake{}
	m := openCard(t, f)
	m.nodeActionCursor = len(nodeCardActions()) - 1
	press(&m, tea.KeyEnter)
	if m.screen != "nodesConfirm" || f.deleted {
		t.Fatal("node deleted before confirmation")
	}
	cmd := press(&m, tea.KeyEnter)
	next, _ := m.Update(cmd())
	m = next.(Model)
	if !f.deleted || m.screen != "nodesResult" {
		t.Fatal("delete did not complete")
	}
	press(&m, tea.KeyEsc)
	if m.screen != "nodesList" || len(m.nodeList) != 0 {
		t.Fatal("deleted node still selected")
	}
}

func TestNodeListRetainsInstallationEntry(t *testing.T) {
	m := openCard(t, &cardNodeFake{})
	press(&m, tea.KeyEsc)
	press(&m, tea.KeyDown)
	press(&m, tea.KeyEnter)
	if m.screen != "nodeConnectInput" {
		t.Fatal("Add node is not reachable from the node list", m.screen)
	}
}

func TestNodeCardKeepsSelectedActionVisible(t *testing.T) {
	m := openCard(t, &cardNodeFake{})
	m.nodeActionCursor = len(nodeCardActions()) - 1
	for _, size := range [][2]int{{36, 18}, {60, 24}, {80, 30}, {124, 40}} {
		m.width, m.height = size[0], size[1]
		if !strings.Contains(m.View(), "Удалить") {
			t.Fatalf("selected action hidden at %dx%d", m.width, m.height)
		}
	}
}
