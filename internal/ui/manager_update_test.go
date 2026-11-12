package ui

import (
	"context"
	"testing"

	"github.com/ConsolePro/RemnaManager/internal/updater"
	tea "github.com/charmbracelet/bubbletea"
)

type managerUpdateFake struct{ applied int }

func (f *managerUpdateFake) Prepare(context.Context, string, bool) (updater.Plan, error) {
	return updater.Plan{Current: "v1.0.0", Version: "v1.2.0"}, nil
}
func (f *managerUpdateFake) Apply(context.Context, updater.Plan) error { f.applied++; return nil }
func TestManagerUpdateRequiresConfirmationAndExplicitRestart(t *testing.T) {
	m := testModel(t, true)
	fake := &managerUpdateFake{}
	m.managerUpdater = fake
	next, cmd := m.openManagerUpdate("manager-update")
	m = next.(Model)
	next, _ = m.Update(cmd())
	m = next.(Model)
	if m.screen != "managerConfirm" || fake.applied != 0 {
		t.Fatal("update applied before confirmation")
	}
	cmd = press(&m, tea.KeyEnter)
	next, _ = m.Update(cmd())
	m = next.(Model)
	if m.screen != "managerResult" || fake.applied != 1 || m.RestartRequested() {
		t.Fatal("update did not wait for explicit restart")
	}
	cmd = press(&m, tea.KeyEnter)
	if cmd == nil || !m.RestartRequested() {
		t.Fatal("restart was not requested")
	}
}
func TestManagerUpdateIgnoresLateResultAfterLeaving(t *testing.T) {
	m := testModel(t, true)
	fake := &managerUpdateFake{}
	m.managerUpdater = fake
	next, cmd := m.openManagerUpdate("manager-update")
	m = next.(Model)
	message := cmd()
	press(&m, tea.KeyEsc)
	next, _ = m.Update(message)
	m = next.(Model)
	if m.screen != "menu" || fake.applied != 0 {
		t.Fatal("late update result reopened the operation")
	}
}
