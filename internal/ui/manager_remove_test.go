package ui

import (
	"context"
	"testing"

	"github.com/ConsolePro/RemnaManager/internal/services"
	tea "github.com/charmbracelet/bubbletea"
)

type removalFake struct {
	liveFake
	preparations, removals int
}

func (f *removalFake) PrepareManagerRemoval(context.Context) (services.ManagerRemovalPlan, error) {
	f.preparations++
	return services.ManagerRemovalPlan{Remove: []string{"/usr/local/lib/remnamanager/remanager"}, Preserve: []string{"/opt/remnawave", "/var/lib/remnamanager"}, Signature: "fixture"}, nil
}
func (f *removalFake) RemoveManager(context.Context, services.ManagerRemovalPlan) error {
	f.removals++
	return nil
}

func TestManagerRemovalRequiresConfirmationAndExitsBeforeDeleting(t *testing.T) {
	m := testModel(t, true)
	f := &removalFake{}
	m.service = f
	next, cmd := m.openManagerUpdate("manager-remove")
	m = next.(Model)
	next, _ = m.Update(cmd())
	m = next.(Model)
	if m.screen != "managerRemoveConfirm" || f.preparations != 1 || f.removals != 0 {
		t.Fatal("removal ran before confirmation")
	}
	if _, requested := m.ManagerRemovalRequested(); requested {
		t.Fatal("unconfirmed removal")
	}
	cmd = press(&m, tea.KeyEnter)
	plan, requested := m.ManagerRemovalRequested()
	if cmd == nil || !requested || plan.Signature != "fixture" || f.removals != 0 {
		t.Fatal("removal must run only after the TUI closes")
	}
}

func TestManagerRemovalCancellationDiscardsLatePlan(t *testing.T) {
	m := testModel(t, true)
	f := &removalFake{}
	m.service = f
	next, cmd := m.openManagerUpdate("manager-remove")
	m = next.(Model)
	press(&m, tea.KeyEsc)
	next, _ = m.Update(cmd())
	m = next.(Model)
	if m.screen != "menu" || f.removals != 0 {
		t.Fatal("cancelled removal reopened")
	}
	if _, requested := m.ManagerRemovalRequested(); requested {
		t.Fatal("cancelled removal requested")
	}
}
