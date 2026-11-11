package ui

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/ConsolePro/RemnaManager/internal/services"
	tea "github.com/charmbracelet/bubbletea"
)

type reportingInstaller struct {
	liveFake
	release chan struct{}
}

func (s *reportingInstaller) Step(ctx context.Context, _ int, _ services.PanelParameters) error {
	services.ReportProgress(ctx, "Обновляю список пакетов", "Updating package lists")
	<-s.release
	return errors.New("installation failed")
}

func TestInstallReportsActionsBeforeStepFinishesAndStopsOnFailure(t *testing.T) {
	m := testModel(t, true)
	s := &reportingInstaller{release: make(chan struct{})}
	m.service, m.screen, m.stage = s, "progress", 1
	m.prepareInstallInputs()
	m.installCtx, m.installCancel = context.WithCancel(m.ctx)
	defer m.cancelInstall()
	result := make(chan tea.Msg, 1)
	go func() { result <- m.nextStep()() }()
	var msg tea.Msg
	select {
	case msg = <-result:
	case <-time.After(3 * time.Second):
		close(s.release)
		t.Fatal("no live action before stage completion")
	}
	next, wait := m.Update(msg)
	m = next.(Model)
	if !strings.Contains(m.body(), "→ Обновляю список пакетов") || m.stage != 1 {
		t.Fatal(m.body())
	}
	before := m.body()
	next, tick := m.Update(installTickMsg{m.run})
	m = next.(Model)
	if tick == nil || before == m.body() {
		t.Fatal("spinner did not advance")
	}
	close(s.release)
	next, cmd := m.Update(wait())
	m = next.(Model)
	if cmd != nil || m.stage != 1 || m.problem == "" || !strings.Contains(m.body(), "✗ Установка зависимостей") {
		t.Fatal(m.body())
	}
	_, tick = m.Update(installTickMsg{m.run})
	if tick != nil {
		t.Fatal("spinner kept running after failure")
	}
}

func TestInstallStageListAndLatestActionFitNormalTerminal(t *testing.T) {
	m := testModel(t, true)
	m.service, m.screen, m.stage = &liveFake{}, "progress", 1
	m.prepareInstallInputs()
	m.width, m.height = 80, 30
	m.logs = []string{"Проверяю пакеты", "Обновляю список пакетов", "Устанавливаю зависимости", "Устанавливаю Docker"}
	view := m.View()
	if !strings.Contains(view, "✓ Проверка параметров") || !strings.Contains(view, "Устанавливаю Docker") {
		t.Fatal(view)
	}
	m.logs = append(m.logs, "Завершено")
	next, _ := m.Update(stepMsg{run: m.run, step: 1})
	m = next.(Model)
	if len(m.logs) != 0 || !strings.Contains(m.body(), "✓ Установка зависимостей") {
		t.Fatal(m.body())
	}
}
