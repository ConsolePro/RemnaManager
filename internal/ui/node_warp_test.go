package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestNativeWarpFlowNeedsOnlyNodeAndConfirmation(t *testing.T) {
	for _, action := range []string{"node-warp-install", "node-warp-remove"} {
		t.Run(action, func(t *testing.T) {
			m := testModel(t, true)
			service := &nodeFake{}
			m.service = service
			next, cmd := m.openNodeOperation(operation("WARP Native", "WARP Native", action))
			m = next.(Model)
			next, _ = m.Update(cmd())
			m = next.(Model)
			cmd = press(&m, tea.KeyEnter)
			next, _ = m.Update(cmd())
			m = next.(Model)
			if m.screen != "nodesConfirm" || len(m.inputs) != 0 {
				t.Fatal("native WARP asked for files or opened a constructor", m.screen)
			}
			cmd = press(&m, tea.KeyEnter)
			next, cmd = m.Update(cmd())
			m = next.(Model)
			if cmd == nil {
				t.Fatal("authentication did not continue installation")
			}
			next, _ = m.Update(cmd())
			m = next.(Model)
			var actions []string
			for _, req := range service.requests {
				actions = append(actions, req.Action)
			}
			if m.screen != "nodesResult" || strings.Join(actions, ",") != "nodes-list,"+action+"-prepare,"+action+"-auth,"+action+"-run" {
				t.Fatal(m.screen, actions)
			}
		})
	}
}
